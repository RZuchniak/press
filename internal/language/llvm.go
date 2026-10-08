package language

//go:generate go run ../../scripts/gen-llvm-step

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// LLVMOptimization selects an LLVM IR optimization pipeline.
type LLVMOptimization string

const (
	LLVMOptNone LLVMOptimization = "none"
	LLVMOptO1   LLVMOptimization = "O1"
	LLVMOptO2   LLVMOptimization = "O2"
)

// LLVMOptions selects the native library and optimization pipeline. An empty
// library uses PRESS_LLVM_DLL or the platform loader's default.
type LLVMOptions struct {
	LibraryPath string
	// Empty selects O2. None keeps IR optimization disabled for comparisons.
	Optimization LLVMOptimization
}

// LLVMProgram owns generated code. Close must be called after the last execution.
// Executions share code but have independent Go-managed guest state.
type LLVMProgram struct {
	mu     sync.RWMutex
	plan   *executionPlan
	native nativeExecutor
	closed bool
}

type nativeExecutor interface {
	run(*jitFrame, uint64) value
	close() error
}

// One registry entry roots an entire execution. Recursive calls use independent
// reusable frames; the active frame changes only during synchronous guest calls.
type jitExecution struct {
	code    nativeExecutor
	id      uint64
	current *jitFrame
	frames  []*jitFrame
	used    int
}

func (s *jitExecution) run(vm *machine, fn *preparedFunction, base int) value {
	if s.used == len(s.frames) {
		s.frames = append(s.frames, &jitFrame{})
	}
	f := s.frames[s.used]
	buffer := f.exchange
	*f = jitFrame{vm: vm, native: s, fn: fn, base: base, exchange: buffer}
	previous := s.current
	s.current = f
	s.used++
	defer func() {
		buffer := f.exchange
		clear(buffer)
		*f = jitFrame{exchange: buffer}
		s.current = previous
		s.used--
	}()
	return s.code.run(f, s.id)
}

// LLVMIR lowers trusted Press bytecode into textual LLVM IR. No LLVM installation
// is needed to inspect the output. Guest heap pointers never appear in the IR.
func LLVMIR(program *Program) (string, error) {
	plan, err := preparePlan(program, false)
	if err != nil {
		return "", err
	}
	ir, _, _ := emitLLVM(plan, program)
	return ir, nil
}

func CompileLLVM(program *Program, options LLVMOptions) (*LLVMProgram, error) {
	if options.Optimization == "" {
		options.Optimization = LLVMOptO2
	}
	if options.Optimization != LLVMOptNone && options.Optimization != LLVMOptO1 && options.Optimization != LLVMOptO2 {
		return nil, fmt.Errorf("Unknown LLVM optimization %q (use none, O1 or O2)", options.Optimization)
	}
	plan, err := preparePlan(program, false)
	if err != nil {
		return nil, err
	}
	ir, symbols, mixed := emitLLVM(plan, program)
	native, err := newLLVMNative(ir, symbols, mixed, options)
	if err != nil {
		return nil, err
	}
	return &LLVMProgram{plan: plan, native: native}, nil
}

func (p *LLVMProgram) Execute(options Options) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return fmt.Errorf("LLVM program is closed")
	}
	return executeLLVMPlan(p.plan, options, p.native)
}

func (p *LLVMProgram) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	err := p.native.close()
	p.native, p.plan = nil, nil
	return err
}

const (
	jitStep = iota
	jitFail
	jitArgument
	jitPrintInt
	jitPrintBool
	jitReturnInt
	jitReturnBool
	jitReturnVoid
	jitBatch
	jitMixedBatch
)
const jitFailed = ^uint64(0)

// This Go-owned registry is the native frame root set. IDs are not Go pointers.
// Windows uses NewCallback rather than cgo; a cgo adapter can use cgo.Handle.
var jitFrames sync.Map
var jitFrameID atomic.Uint64

type jitFrame struct {
	vm       *machine
	native   *jitExecution
	fn       *preparedFunction
	base     int
	result   value
	err      error
	mixed    *mixedAnalysis
	exchange []int64
}

// All exceptions stop at this callback boundary. Native frames return normally,
// then Go propagates the captured error through its ordinary frame cleanup.
func jitCallback(id, pc, action, payload uint64) (status uint64) {
	entry, ok := jitFrames.Load(id)
	if !ok {
		return jitFailed
	}
	f := entry.(*jitExecution).current
	if f == nil {
		return jitFailed
	}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(languageError); ok {
				f.err = e
			} else {
				f.err = fmt.Errorf("Invalid bytecode: %v", r)
			}
			status = jitFailed
		}
	}()
	if f.err != nil {
		return jitFailed
	}
	if action == jitMixedBatch {
		if f.mixed == nil || pc >= uint64(len(f.fn.code)) || payload == 0 || payload > 16 {
			fail("Invalid mixed instruction batch")
		}
		f.vm.steps = int(f.exchange[0])
		defer func() { f.exchange[0] = int64(f.vm.steps) }()
		f.mixedImport(int(pc))
		next := pc
		for offset := uint64(0); offset < payload; offset++ {
			next = jitStepFrame(f, pc+offset)
			if next != pc+offset+1 || f.err != nil {
				break
			}
		}
		if next < uint64(len(f.fn.code)) {
			f.mixedExport(int(next))
		}
		return next
	}
	if action == jitBatch {
		if payload == 0 || payload > 16 {
			fail("Invalid native instruction batch")
		}
		for offset := uint64(0); offset < payload; offset++ {
			next := jitStepFrame(f, pc+offset)
			if next != pc+offset+1 || f.err != nil {
				return next
			}
		}
		return pc + payload
	}
	if action == jitFail {
		switch payload {
		case 1:
			fail("i64 overflow")
		case 2:
			fail("Division by zero")
		case 4:
			fail("Instruction limit exceeded")
		case 5:
			fail("Call depth limit exceeded")
		default:
			fail("Function " + f.fn.name + " fell through")
		}
	}
	if pc >= uint64(len(f.fn.code)) {
		fail("Invalid native instruction index")
	}
	switch action {
	case jitArgument:
		if payload >= uint64(f.fn.params) {
			fail("Invalid native argument")
		}
		return uint64(f.vm.locals[f.base+int(payload)].number)
	case jitPrintInt, jitPrintBool:
		kind := intValue
		if action == jitPrintBool {
			kind = boolValue
		}
		f.vm.options.Print(formatValue(value{kind: kind, number: int64(payload)}))
		return 0
	case jitReturnInt, jitReturnBool, jitReturnVoid:
		kind := intValue
		if action == jitReturnBool {
			kind = boolValue
		}
		if action == jitReturnVoid {
			kind = invalidValue
		}
		f.result = value{kind: kind, number: int64(payload)}
		return 0
	case jitStep:
		return jitStepFrame(f, pc)
	default:
		fail("Invalid native callback action")
	}
	return jitFailed
}

// Keep the native path separate so foreign calls do not change escape analysis
// or dispatch costs in the default VM.
func executeLLVMPlan(plan *executionPlan, options Options, native nativeExecutor) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(languageError); ok {
				err = e
			} else {
				err = fmt.Errorf("Invalid bytecode: %v", r)
			}
		}
	}()
	if options.Print == nil {
		options.Print = func(s string) { fmt.Println(s) }
	}
	if options.MaxSteps <= 0 {
		options.MaxSteps = 1_000_000
	}
	if options.MaxDepth <= 0 {
		options.MaxDepth = 256
	}
	buffers := scratchPool.Get().(*scratchBuffers)
	vm := &machine{plan: plan, options: options, locals: buffers.locals[:0], stack: buffers.stack[:0]}
	session := &jitExecution{code: native, id: jitFrameID.Add(1)}
	jitFrames.Store(session.id, session)
	defer jitFrames.Delete(session.id)
	defer func() {
		clear(vm.locals[:cap(vm.locals)])
		clear(vm.stack[:cap(vm.stack)])
		if cap(vm.locals) <= 1024 && cap(vm.stack) <= 1024 {
			buffers.locals, buffers.stack = vm.locals[:0], vm.stack[:0]
			scratchPool.Put(buffers)
		}
	}()
	runLLVM(vm, plan.functions["main"], nil, session)
	return nil
}

func runLLVM(vm *machine, fn *preparedFunction, args []value, native *jitExecution) value {
	vm.depth++
	defer func() { vm.depth-- }()
	if vm.depth > vm.options.MaxDepth {
		fail("Call depth limit exceeded")
	}
	if fn == nil {
		fail("Missing function")
	}
	if len(args) != fn.params {
		fail("Wrong bytecode argument count for " + fn.name)
	}
	base, stackBase, previous := len(vm.locals), len(vm.stack), vm.frameBase
	vm.frameBase = stackBase
	for i := 0; i < fn.locals; i++ {
		vm.locals = append(vm.locals, value{})
	}
	for i, arg := range args {
		if i == 0 && fn.receiver {
			object(arg)
			arg.kind = structValue
		}
		vm.locals[base+i] = arg
	}
	defer func() {
		vm.frameBase = previous
		clear(vm.locals[base:])
		vm.locals = vm.locals[:base]
		if len(vm.stack) > stackBase {
			clear(vm.stack[stackBase:])
			vm.stack = vm.stack[:stackBase]
		}
	}()
	return native.run(vm, fn, base)
}

func jitStepFrame(f *jitFrame, pc uint64) uint64 {
	if pc >= uint64(len(f.fn.code)) {
		fail("Invalid native instruction index")
	}
	i := &f.fn.code[pc]

	f.vm.steps++
	if f.vm.steps > f.vm.options.MaxSteps {
		fail("Instruction limit exceeded")
	}
	if i.op == codeCall || i.op == codeCallMethod {
		count, target := i.count, i.target
		if i.op == codeCallMethod {
			count++
		}
		values := f.vm.take(count)
		if i.op == codeCallMethod {
			target = object(values[0]).layout.methods[i.text]
			if target == nil {
				fail("Missing method dispatch target")
			}
		}
		result := runLLVM(f.vm, target, values, f.native)
		if target.result {
			f.vm.stack = append(f.vm.stack, result)
		}
		return pc + 1
	}
	next, result, returned := f.vm.step(i, f.base, int(pc)+1)
	if returned {
		f.result = result
		return uint64(len(f.fn.code) + 1)
	}
	return uint64(next)
}
