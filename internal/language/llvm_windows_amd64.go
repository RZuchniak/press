package language

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Go's supported Windows foreign-call and callback machinery performs the
// runtime transitions. Native code sees scalar IDs, never guest heap pointers.
var windowsJITCallback = sync.OnceValue(func() uintptr {
	return syscall.NewCallback(func(id, pc, action, payload uintptr) uintptr {
		return uintptr(jitCallback(uint64(id), uint64(pc), uint64(action), uint64(payload)))
	})
})

type windowsLLVM struct {
	dll       *syscall.DLL
	procs     map[string]*syscall.Proc
	jit       uintptr
	functions map[*preparedFunction]uintptr
	mixed     map[*preparedFunction]*mixedAnalysis
}

func newLLVMNative(ir string, symbols map[*preparedFunction]string, mixed map[*preparedFunction]*mixedAnalysis, options LLVMOptions) (nativeExecutor, error) {
	path := options.LibraryPath
	if path == "" {
		path = os.Getenv("PRESS_LLVM_DLL")
	}
	if path == "" {
		path = "LLVM-C.dll"
		local := filepath.Join(".tools", "llvm", "bin", path)
		if _, err := os.Stat(local); err == nil {
			path, _ = filepath.Abs(local)
		}
	}
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return nil, fmt.Errorf("Load LLVM library (set PRESS_LLVM_DLL to LLVM-C.dll): %w", err)
	}
	l := &windowsLLVM{dll: dll, procs: map[string]*syscall.Proc{}, functions: map[*preparedFunction]uintptr{}, mixed: mixed}
	names := []string{
		"LLVMInitializeX86TargetInfo", "LLVMInitializeX86Target", "LLVMInitializeX86TargetMC", "LLVMInitializeX86AsmPrinter",
		"LLVMOrcCreateLLJIT", "LLVMOrcDisposeLLJIT", "LLVMOrcLLJITGetMainJITDylib", "LLVMOrcLLJITAddLLVMIRModule", "LLVMOrcLLJITLookup",
		"LLVMContextCreate", "LLVMOrcCreateNewThreadSafeContextFromLLVMContext", "LLVMOrcDisposeThreadSafeContext", "LLVMOrcCreateNewThreadSafeModule",
		"LLVMCreateMemoryBufferWithMemoryRangeCopy", "LLVMParseIRInContext", "LLVMVerifyModule", "LLVMDisposeModule", "LLVMDisposeMessage",
		"LLVMGetErrorMessage", "LLVMDisposeErrorMessage", "LLVMSetTarget", "LLVMSetDataLayout", "LLVMOrcLLJITGetTripleString", "LLVMOrcLLJITGetDataLayoutStr",
		"LLVMCreatePassBuilderOptions", "LLVMDisposePassBuilderOptions", "LLVMPassBuilderOptionsSetVerifyEach", "LLVMRunPasses",
	}
	for _, name := range names {
		proc, err := dll.FindProc(name)
		if err != nil {
			dll.Release()
			return nil, fmt.Errorf("LLVM library missing %s: %w", name, err)
		}
		l.procs[name] = proc
	}
	for _, name := range names[:4] {
		l.call(name)
	}
	var jit uintptr
	if e := l.call("LLVMOrcCreateLLJIT", &jit, uintptr(0)); e != 0 {
		err := l.llvmError(e)
		dll.Release()
		return nil, err
	}
	l.jit = jit
	ok := false
	defer func() {
		if !ok {
			l.close()
		}
	}()
	context := l.call("LLVMContextCreate")
	tsc := l.call("LLVMOrcCreateNewThreadSafeContextFromLLVMContext", context)
	defer l.call("LLVMOrcDisposeThreadSafeContext", tsc)
	buffer := l.call("LLVMCreateMemoryBufferWithMemoryRangeCopy", []byte(ir), uintptr(len(ir)), "press")
	var module, message uintptr
	if l.call("LLVMParseIRInContext", context, buffer, &module, &message) != 0 {
		err := fmt.Errorf("LLVM IR parse: %s", nativeString(message))
		l.call("LLVMDisposeMessage", message)
		return nil, err
	}
	ownedModule := true
	defer func() {
		if ownedModule {
			l.call("LLVMDisposeModule", module)
		}
	}()
	l.call("LLVMSetTarget", module, l.call("LLVMOrcLLJITGetTripleString", jit))
	l.call("LLVMSetDataLayout", module, l.call("LLVMOrcLLJITGetDataLayoutStr", jit))
	message = 0
	if l.call("LLVMVerifyModule", module, uintptr(2), &message) != 0 {
		err := fmt.Errorf("LLVM IR verification: %s", nativeString(message))
		l.call("LLVMDisposeMessage", message)
		return nil, err
	}
	if message != 0 {
		l.call("LLVMDisposeMessage", message)
	}
	if options.Optimization != LLVMOptNone {
		passOptions := l.call("LLVMCreatePassBuilderOptions")
		l.call("LLVMPassBuilderOptionsSetVerifyEach", passOptions, uintptr(1))
		errRef := l.call("LLVMRunPasses", module, "default<"+string(options.Optimization)+">", uintptr(0), passOptions)
		l.call("LLVMDisposePassBuilderOptions", passOptions)
		if errRef != 0 {
			return nil, l.llvmError(errRef)
		}
	}
	tsm := l.call("LLVMOrcCreateNewThreadSafeModule", module, tsc)
	ownedModule = false
	jd := l.call("LLVMOrcLLJITGetMainJITDylib", jit)
	if e := l.call("LLVMOrcLLJITAddLLVMIRModule", jit, jd, tsm); e != 0 {
		return nil, l.llvmError(e)
	}
	for fn, symbol := range symbols {
		var address uintptr
		if e := l.call("LLVMOrcLLJITLookup", jit, &address, symbol); e != 0 {
			return nil, l.llvmError(e)
		}
		l.functions[fn] = address
	}
	ok = true
	return l, nil
}

// Pin pointer-free Go buffers and scalar out parameters for each LLVM API call.
// LLVM keeps only its own copies/allocations after these calls return.
func (l *windowsLLVM) call(name string, args ...any) uintptr {
	var pins runtime.Pinner
	defer pins.Unpin()
	native := make([]uintptr, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case uintptr:
			native[i] = v
		case *uintptr:
			pins.Pin(v)
			native[i] = uintptr(unsafe.Pointer(v))
		case []byte:
			if len(v) == 0 {
				v = []byte{0}
			}
			pins.Pin(&v[0])
			native[i] = uintptr(unsafe.Pointer(&v[0]))
		case string:
			vbytes, err := syscall.ByteSliceFromString(v)
			if err != nil {
				panic(err)
			}
			pins.Pin(&vbytes[0])
			native[i] = uintptr(unsafe.Pointer(&vbytes[0]))
		default:
			panic("Unsupported LLVM FFI argument")
		}
	}
	result, _, _ := syscall.SyscallN(l.procs[name].Addr(), native...)
	runtime.KeepAlive(args)
	return result
}

// Copy native-owned error text without turning native integer addresses into
// Go heap pointers. These Windows calls do not retain the destination buffer.
func nativeString(address uintptr) string {
	if address == 0 {
		return ""
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	n, _, _ := kernel.NewProc("lstrlenA").Call(address)
	if n == 0 {
		return ""
	}
	bytes := make([]byte, n)
	kernel.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&bytes[0])), address, n)
	runtime.KeepAlive(bytes)
	return string(bytes)
}

func (l *windowsLLVM) llvmError(e uintptr) error {
	message := l.call("LLVMGetErrorMessage", e)
	err := fmt.Errorf("LLVM: %s", nativeString(message))
	l.call("LLVMDisposeErrorMessage", message)
	return err
}

func (l *windowsLLVM) run(f *jitFrame, id uint64) value {
	vm, fn := f.vm, f.fn
	f.mixed = l.mixed[fn]
	// Pointer-free budget storage is pinned for the entire native invocation.
	size := 4
	if f.mixed != nil {
		size += fn.locals + f.mixed.stackSize
	}
	budget := f.exchange
	if cap(budget) < size {
		budget = make([]int64, size)
	} else {
		budget = budget[:size]
	}
	copy(budget, []int64{int64(vm.steps), int64(vm.options.MaxSteps), int64(vm.depth), int64(vm.options.MaxDepth)})
	f.exchange = budget
	if f.mixed != nil {
		f.mixedExport(0)
	}
	var pin runtime.Pinner
	pin.Pin(&budget[0])
	defer pin.Unpin()
	before := budget[0]
	status, _, _ := syscall.SyscallN(l.functions[fn], uintptr(id), windowsJITCallback(), uintptr(unsafe.Pointer(&budget[0])))
	if budget[0] != before {
		vm.steps = int(budget[0])
	}
	runtime.KeepAlive(budget)
	if f.err != nil {
		panic(languageError{f.err.Error()})
	}
	if status != 0 {
		fail("Native execution failed")
	}
	return f.result
}

func (l *windowsLLVM) close() error {
	var err error
	if l.jit != 0 {
		if e := l.call("LLVMOrcDisposeLLJIT", l.jit); e != 0 {
			err = l.llvmError(e)
		}
		l.jit = 0
	}
	if release := l.dll.Release(); err == nil {
		err = release
	}
	return err
}
