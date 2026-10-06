package language

import (
	"fmt"
	"math"
	"sync"
)

type Options struct {
	Print              func(string)
	MaxSteps, MaxDepth int
}
type machine struct {
	plan          *executionPlan
	options       Options
	steps, depth  int
	frameBase     int
	locals, stack []value
}
type scratchBuffers struct{ locals, stack []value }

var scratchPool = sync.Pool{New: func() any { return &scratchBuffers{locals: make([]value, 0, 16), stack: make([]value, 0, 16)} }}

// Execute runs trusted bytecode, not a sandbox. Metadata must remain immutable
// after first execution. Concurrent runs of the same Program are safe.
func Execute(program *Program, options Options) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(languageError); ok {
				err = e
			} else {
				err = fmt.Errorf("Invalid bytecode: %v", r)
			}
		}
	}()
	validateHeader(program)
	program.prepareOnce.Do(func() { program.prepared, program.prepareError = prepare(program) })
	if program.prepareError != nil {
		return program.prepareError
	}
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
	vm := machine{plan: program.prepared, options: options, locals: buffers.locals[:0], stack: buffers.stack[:0]}
	defer func() {
		// Drop guest references even in unused capacity and on error. Avoid retaining
		// oversized arenas after unusually deep calls or large expressions.
		clear(vm.locals[:cap(vm.locals)])
		clear(vm.stack[:cap(vm.stack)])
		if cap(vm.locals) <= 1024 && cap(vm.stack) <= 1024 {
			buffers.locals = vm.locals[:0]
			buffers.stack = vm.stack[:0]
			scratchPool.Put(buffers)
		}
	}()
	vm.run(vm.plan.functions["main"], nil)
	return nil
}
func (vm *machine) pop() value {
	n := len(vm.stack)
	if n <= vm.frameBase {
		fail("Bytecode stack underflow")
	}
	v := vm.stack[n-1]
	vm.stack[n-1] = value{}
	vm.stack = vm.stack[:n-1]
	return v
}
func (vm *machine) top() value {
	if len(vm.stack) <= vm.frameBase {
		fail("Bytecode stack underflow")
	}
	return vm.stack[len(vm.stack)-1]
}
func (vm *machine) take(count int) []value {
	if count < 0 || count > len(vm.stack)-vm.frameBase {
		fail("Bytecode stack underflow")
	}
	start := len(vm.stack) - count
	values := vm.stack[start:]
	vm.stack = vm.stack[:start]
	return values
}
func pathValue(v value, path []string) value {
	for _, field := range path {
		o := object(v)
		index, ok := o.layout.fields[field]
		if !ok {
			fail("Missing field " + field)
		}
		v = o.fields[index]
	}
	return v
}
func (vm *machine) run(fn *preparedFunction, args []value) value {
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
	base, stackBase := len(vm.locals), len(vm.stack)
	previousFrame := vm.frameBase
	vm.frameBase = stackBase
	// Reuse a local arena across calls and resolve lexical scopes in the plan.
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
		vm.frameBase = previousFrame
		clear(vm.locals[base:])
		vm.locals = vm.locals[:base]
		if len(vm.stack) > stackBase {
			clear(vm.stack[stackBase:])
			vm.stack = vm.stack[:stackBase]
		}
	}()
	pc := 0
	for pc < len(fn.code) {
		instruction := &fn.code[pc]
		if instruction.span > 1 && instruction.span > vm.options.MaxSteps-vm.steps {
			instruction = instruction.fallback
		}
		pc += instruction.span
		vm.steps += instruction.span
		if vm.steps > vm.options.MaxSteps {
			fail("Instruction limit exceeded")
		}
		switch instruction.op {
		case codeCollection:
			values := vm.take(instruction.count)
			result := newCollection(instruction.collection, values)
			clear(values)
			vm.stack = append(vm.stack, result)
		case codeCallCollection:
			values := vm.take(instruction.count + 1)
			result := collectionCall(instruction.collectionOp, values)
			clear(values)
			if instruction.hasResult {
				vm.stack = append(vm.stack, result)
			}
		case codePush:
			vm.stack = append(vm.stack, instruction.constant)
		case codeLoad:
			vm.stack = append(vm.stack, copyValue(vm.locals[base+instruction.slot]))
		case codeReadPath:
			vm.stack = append(vm.stack, copyValue(pathValue(vm.locals[base+instruction.slot], instruction.path)))
		case codeLoadPath:
			vm.stack = append(vm.stack, pathValue(vm.locals[base+instruction.slot], instruction.path))
		case codeDeclare, codeStoreLocal:
			vm.locals[base+instruction.slot] = vm.pop()
		case codeStoreField:
			v := vm.pop()
			parent := pathValue(vm.locals[base+instruction.slot], instruction.path[:len(instruction.path)-1])
			o := object(parent)
			index, ok := o.layout.fields[instruction.path[len(instruction.path)-1]]
			if !ok {
				fail("Missing field")
			}
			o.fields[index] = v
		case codeStruct:
			fields := make([]value, len(instruction.layout.fields))
			values := vm.take(len(instruction.indices))
			for i, index := range instruction.indices {
				fields[index] = values[i]
			}
			clear(values)
			vm.stack = append(vm.stack, value{kind: structValue, object: &objectValue{layout: instruction.layout, fields: fields}})
		case codeRef:
			v := vm.pop()
			object(v)
			v.kind = referenceValue
			vm.stack = append(vm.stack, v)
		case codeEnum:
			values := vm.take(instruction.count)
			fields := make([]value, len(values))
			copy(fields, values)
			clear(values)
			vm.stack = append(vm.stack, value{kind: enumValue, object: &objectValue{variant: instruction.text, fields: fields}})
		case codeMatch:
			if vm.top().object.variant != instruction.text {
				pc = instruction.jump
			}
		case codePayload:
			vm.stack = append(vm.stack, copyValue(vm.top().object.fields[instruction.count]))
		case codeTry:
			v := vm.pop()
			if v.object.variant != instruction.text {
				return v
			}
			vm.stack = append(vm.stack, v.object.fields[0])
		case codeTrap:
			fail(instruction.text)
		case codeField:
			parent := vm.pop()
			v := pathValue(parent, instruction.path)
			if parent.kind == referenceValue {
				v = copyValue(v)
			}
			vm.stack = append(vm.stack, v)
		case codeUnary:
			v := vm.pop()
			if instruction.text == "!" {
				if v.kind != boolValue {
					fail("Expected bool")
				}
				v = boolean(v.number == 0)
			} else {
				if v.kind != intValue {
					fail("Expected i64")
				}
				if v.number == math.MinInt64 {
					fail("i64 overflow")
				}
				v.number = -v.number
			}
			vm.stack = append(vm.stack, v)
		case codeBinary:
			right, left := vm.pop(), vm.pop()
			vm.stack = append(vm.stack, arithmetic(instruction.arithmetic, left, right))
		case codeBinaryLocal, codeBinaryPush, codeCompareJump:
			left := vm.locals[base+instruction.slot]
			right := instruction.constant
			if !instruction.rightConstant {
				right = vm.locals[base+instruction.rightSlot]
			}
			result := arithmetic(instruction.arithmetic, left, right)
			switch instruction.op {
			case codeBinaryLocal:
				vm.locals[base+instruction.count] = result
			case codeBinaryPush:
				vm.stack = append(vm.stack, result)
			case codeCompareJump:
				if result.number == 0 {
					pc = instruction.jump
				}
			}
		case codeAnd:
			if vm.top().number == 0 {
				pc = instruction.jump
			} else {
				vm.pop()
			}
		case codeOr:
			if vm.top().number != 0 {
				pc = instruction.jump
			} else {
				vm.pop()
			}
		case codePrint:
			vm.options.Print(formatValue(vm.pop()))
		case codeCall:
			values := vm.take(instruction.count)
			result := vm.run(instruction.target, values)
			if instruction.target.result {
				vm.stack = append(vm.stack, result)
			}
		case codeCallMethod:
			values := vm.take(instruction.count + 1)
			o := object(values[0])
			target := o.layout.methods[instruction.text]
			if target == nil {
				fail("Missing method dispatch target")
			}
			result := vm.run(target, values)
			if target.result {
				vm.stack = append(vm.stack, result)
			}
		case codeReturn:
			if instruction.count != 0 {
				return vm.pop()
			}
			return value{}
		case codePop:
			vm.pop()
		case codeEnter: // no runtime scope maps
		case codeExit:
			for _, slot := range instruction.indices {
				vm.locals[base+slot] = value{}
			}
		case codeJump:
			pc = instruction.jump
		case codeJumpFalse:
			if vm.pop().number == 0 {
				pc = instruction.jump
			}
		default:
			fail("Unknown opcode")
		}
	}
	fail("Function " + fn.name + " fell through")
	return value{}
}
