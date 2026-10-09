package language

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"press/internal/bench"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

func llvmTestOptions(t testing.TB) LLVMOptions {
	t.Helper()
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("Native adapter requires Windows/amd64")
	}
	path := os.Getenv("PRESS_LLVM_DLL")
	if path == "" {
		path, _ = filepath.Abs(filepath.Join("..", "..", ".tools", "llvm", "bin", "LLVM-C.dll"))
		if _, err := os.Stat(path); err != nil {
			t.Skip("Install LLVM or set PRESS_LLVM_DLL to run native tests")
		}
	}
	return LLVMOptions{LibraryPath: path}
}

func TestLLVMIRPortableAndValidation(t *testing.T) {
	p, err := Compile(`fn main() { println(40 + 2); }`)
	if err != nil {
		t.Fatal(err)
	}
	ir, err := LLVMIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"define i64 @press_f0", "llvm.sadd.with.overflow.i64", "call i64 %callback"} {
		if !strings.Contains(ir, fragment) {
			t.Fatalf("Missing %s", fragment)
		}
	}
	if _, err := LLVMIR(nil); err == nil {
		t.Fatal("Accepted nil bytecode")
	}
	p.Functions["main"].Code[0] = Instruction{"UNKNOWN"}
	if _, err := LLVMIR(p); err == nil {
		t.Fatal("Accepted malformed bytecode")
	}
}

func compareLLVM(t *testing.T, p *Program, native *LLVMProgram, options Options) {
	t.Helper()
	var want, got []string
	options.Print = func(s string) { want = append(want, s) }
	wantErr := Execute(p, options)
	options.Print = func(s string) { got = append(got, s) }
	gotErr := native.Execute(options)
	if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !slices.Equal(want, got) {
		t.Fatalf("VM: %q / %v; LLVM: %q / %v (steps=%d depth=%d)", want, wantErr, got, gotErr, options.MaxSteps, options.MaxDepth)
	}
}

func TestLLVMHistoricalRegressions(t *testing.T) {
	config := llvmTestOptions(t)
	var cases []struct {
		Source, CompileError string
		Executions           []struct{ MaxSteps int }
	}
	data, err := os.ReadFile("testdata/regressions.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for index, c := range cases {
		if c.CompileError != "" {
			continue
		}
		t.Run(fmt.Sprintf("case_%02d", index+1), func(t *testing.T) {
			p, err := Compile(c.Source)
			if err != nil {
				t.Fatal(err)
			}
			j, err := CompileLLVM(p, config)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			for _, e := range c.Executions {
				compareLLVM(t, p, j, Options{MaxSteps: e.MaxSteps})
			}
		})
	}
}

func TestLLVMDifferentialBudgetsAndArithmetic(t *testing.T) {
	config := llvmTestOptions(t)
	sources := []string{
		`fn main() { let n=0; while (n<5) { n+=1; } println(n); }`,
		`fn inc(n: i64) -> i64 { return n + 1; } fn main() { let n = 0; while (n < 5) { n = inc(n); } println(n); println(true && false || true); }`,
		`fn main() { println(-9223372036854775808 % -1); println(-7 / 3); println(-7 % 3); println(3 * -4); println(4 >= 4); println(true != false); println("a" + "b"); }`,
		`fn main() { println(9223372036854775807 + 1); }`,
		`fn main() { println(-9223372036854775808 - 1); }`,
		`fn main() { println(-9223372036854775808 * -1); }`,
		`fn main() { println(-9223372036854775808 / -1); }`,
		`fn main() { println(1 / 0); }`,
		`fn main() { println(1 % 0); }`,
		`fn forever() { forever(); } fn main() { forever(); }`,
		`fn main() { println(false && (1 / 0 == 1)); println(true || (1 / 0 == 1)); }`,
	}
	for index, source := range sources {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			p, err := Compile(source)
			if err != nil {
				t.Fatal(err)
			}
			j, err := CompileLLVM(p, config)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			for steps := 1; steps <= 100; steps++ {
				compareLLVM(t, p, j, Options{MaxSteps: steps, MaxDepth: 8})
			}
		})
	}
}

func TestLLVMScalarEligibility(t *testing.T) {
	for _, c := range []struct {
		source string
		scalar bool
	}{
		{`fn main() { let n=0; while (n<5) { n+=1; } println(n); }`, true},
		{`fn main() { println(true && false || true); }`, true},
		{`fn main() { println("hello"); }`, false},
		{`fn answer() -> i64 { return 42; } fn main() { println(answer()); }`, false},
	} {
		p, err := Compile(c.source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := preparePlan(p, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := scalarStates(plan.functions["main"], p.Functions["main"]) != nil; got != c.scalar {
			t.Fatalf("Scalar=%v, expected %v: %s", got, c.scalar, c.source)
		}
	}
}

func TestLLVMExamplesUnderGC(t *testing.T) {
	config := llvmTestOptions(t)
	for _, file := range []string{"counter.press", "results.press", "interfaces.press", "tasks/main.press"} {
		t.Run(file, func(t *testing.T) {
			p, err := CompileFile(filepath.Join("..", "..", "examples", file))
			if err != nil {
				t.Fatal(err)
			}
			j, err := CompileLLVM(p, config)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			var want, got []string
			if err := Execute(p, Options{Print: func(s string) { want = append(want, s) }}); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 3; attempt++ {
				got = nil
				err := j.Execute(Options{Print: func(s string) { runtime.GC(); got = append(got, s) }})
				if err != nil || !slices.Equal(want, got) {
					t.Fatalf("%q / %v; expected %q", got, err, want)
				}
			}
		})
	}
}

func TestLLVMDirectCallsAndBatchBoundaries(t *testing.T) {
	config := llvmTestOptions(t)
	sources := []string{
		`fn add(a:i64,b:i64)->i64 { return a+b; } fn positive(n:i64)->bool { return n>0; } fn report(n:i64) { let i=0; while (i<3) { i+=1; } println(n+i); } fn main() { println(add(add(2,3),7)); println(positive(-1)); report(10); }`,
		`fn even(n:i64)->bool { if (n==0) { return true; } return odd(n-1); } fn odd(n:i64)->bool { if (n==0) { return false; } return even(n-1); } fn main() { println(even(6)); println(odd(5)); }`,
		`fn factorial(n:i64)->i64 { if (n<2) { return 1; } return n*factorial(n-1); } fn main() { println(factorial(6)); println(factorial(21)); }`,
		`fn emit()->i64 { println("inside helper"); return 42; } fn outer()->i64 { return emit(); } fn main() { println(outer()); }`,
		`struct Item { let n:i64; } fn value(n:i64)->i64 { return n+1; } fn main() { const item=&Item { n:5 }; println(value(item.n)); item.n+=value(1); println(item.n); }`,
		`fn main() { println("before"); println(9223372036854775807+1); println("after"); }`,
		`fn main() { const values=&List<i64>(1,2); let i=0; while (i<3) { println(match (values.get(i)) { Some(n)=>n, None=>-1 }); i+=1; } }`,
		`fn missing()->Option<i64> { return None; } fn read()->Option<i64> { const x=missing()?; println("unreachable"); return Some(x); } fn main() { println(match (read()) { Some(n)=>n, None=>-1 }); }`,
	}
	for index, source := range sources {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			p, err := Compile(source)
			if err != nil {
				t.Fatal(err)
			}
			j, err := CompileLLVM(p, config)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			for steps := 1; steps <= 200; steps++ {
				compareLLVM(t, p, j, Options{MaxSteps: steps})
			}
			for depth := 1; depth <= 10; depth++ {
				compareLLVM(t, p, j, Options{MaxDepth: depth})
			}
			var want, got []string
			wantErr := Execute(p, Options{Print: func(s string) { want = append(want, s) }})
			gotErr := j.Execute(Options{Print: func(s string) { runtime.GC(); got = append(got, s) }})
			if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !slices.Equal(want, got) {
				t.Fatalf("GC execution: %q / %v, expected %q / %v", got, gotErr, want, wantErr)
			}
		})
	}
	jitFrames.Range(func(_, _ any) bool { t.Error("Leaked frame after nested errors"); return true })
}

func TestLLVMCallGraphEligibility(t *testing.T) {
	for _, c := range []struct {
		source string
		native bool
	}{
		{`fn answer()->i64 { return 42; } fn main() { println(answer()); }`, true},
		{`fn first(n:i64)->i64 { if (n==0) { return 0; } return second(n-1); } fn second(n:i64)->i64 { return first(n); } fn main() { println(first(4)); }`, true},
		{`fn answer()->i64 { println("hello"); return 42; } fn indirect()->i64 { return answer(); } fn main() { println(indirect()); }`, false},
	} {
		p, err := Compile(c.source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := preparePlan(p, false)
		if err != nil {
			t.Fatal(err)
		}
		ir, symbols, _ := emitLLVM(plan, p)
		body := "define internal i64 @" + symbols[plan.functions["main"]] + "_scalar"
		if strings.Contains(ir, body) != c.native {
			t.Fatalf("Call graph eligibility expected %v: %s", c.native, c.source)
		}
	}
}

func TestLLVMFrameRootLifetime(t *testing.T) {
	// A synthetic root is reachable only through the native frame registry when
	// GC runs. After a scope exit it must become collectible before execution ends.
	vm := &machine{options: Options{MaxSteps: 100}}
	fn := &preparedFunction{code: []preparedInstruction{{op: codeExit, indices: []int{0}}}}
	frame := &jitFrame{vm: vm, fn: fn}
	id := jitFrameID.Add(1)
	jitFrames.Store(id, &jitExecution{current: frame})
	defer jitFrames.Delete(id)
	collected := make(chan struct{}, 1)
	func() {
		o := &objectValue{fields: []value{{kind: intValue, number: 42}}}
		runtime.SetFinalizer(o, func(*objectValue) { collected <- struct{}{} })
		vm.locals = []value{{kind: referenceValue, object: o}}
	}()
	runtime.GC()
	select {
	case <-collected:
		t.Fatal("Collected live native root")
	default:
	}
	if jitCallback(id, 0, jitStep, 0) != 1 {
		t.Fatal("Scope exit failed")
	}
	for attempt := 0; attempt < 20; attempt++ {
		runtime.GC()
		select {
		case <-collected:
			return
		default:
			runtime.Gosched()
		}
	}
	t.Fatal("Dead root retained during execution")
}

func TestLLVMConcurrencyErrorsAndClose(t *testing.T) {
	config := llvmTestOptions(t)
	p, err := Compile(`struct Box { let n: i64; } fn main() { const b = &Box { n: 1 }; b.n += 41; println(b.n); }`)
	if err != nil {
		t.Fatal(err)
	}
	j, err := CompileLLVM(p, config)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for repeat := 0; repeat < 5; repeat++ {
				var output []string
				if err := j.Execute(Options{Print: func(s string) { output = append(output, s) }}); err != nil || !slices.Equal(output, []string{"42"}) {
					t.Errorf("%q / %v", output, err)
				}
			}
		}()
	}
	group.Wait()
	if err := j.Execute(Options{Print: func(string) { panic("callback failed") }}); err == nil || !strings.Contains(err.Error(), "callback failed") {
		t.Fatal(err)
	}
	if err := j.Execute(Options{MaxSteps: 1}); err == nil {
		t.Fatal("Expected limit error")
	}
	jitFrames.Range(func(_, _ any) bool { t.Error("Leaked native frame"); return true })
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Execute(Options{}); err == nil {
		t.Fatal("Executed closed JIT")
	}
}

func BenchmarkLLVMArithmetic(b *testing.B) {
	p, err := Compile(`fn main() { let n = 0; while (n < 1000) { n += 1; } }`)
	if err != nil {
		b.Fatal(err)
	}
	j, err := CompileLLVM(p, llvmTestOptions(b))
	if err != nil {
		b.Fatal(err)
	}
	defer j.Close()
	for _, backend := range []string{"vm", "llvm"} {
		b.Run(backend, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				if backend == "vm" {
					err = Execute(p, Options{})
				} else {
					err = j.Execute(Options{})
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLLVMWorkloads(b *testing.B) {
	config := llvmTestOptions(b)
	for _, workload := range bench.Workloads() {
		b.Run(workload.Name, func(b *testing.B) {
			p, err := Compile(workload.Source)
			if err != nil {
				b.Fatal(err)
			}
			j, err := CompileLLVM(p, config)
			if err != nil {
				b.Fatal(err)
			}
			defer j.Close()
			for _, backend := range []string{"vm", "llvm"} {
				b.Run(backend, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var output []string
						options := Options{Print: func(s string) { output = append(output, s) }}
						var err error
						if backend == "vm" {
							err = Execute(p, options)
						} else {
							err = j.Execute(options)
						}
						if err != nil || !slices.Equal(output, []string{workload.Expected}) {
							b.Fatalf("%q / %v", output, err)
						}
					}
				})
			}
		})
	}
}

func BenchmarkLLVMCompile(b *testing.B) {
	config := llvmTestOptions(b)
	p, err := Compile(`fn main() { let n=0; while (n<1000) { n+=1; } }`)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j, err := CompileLLVM(p, config)
		if err != nil {
			b.Fatal(err)
		}
		if err := j.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
