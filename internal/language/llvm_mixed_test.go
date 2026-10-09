package language

import (
	"fmt"
	"runtime"
	"slices"
	"testing"
)

const mixedNumericLoop = `struct Box { let n:i64; }
fn main() { const box=&Box { n:42 }; let i=0; let sum=0;
 while (i<1000) { sum+=i; i+=1; } println(sum); println(box.n); }`

func TestLLVMMixedFunctions(t *testing.T) {
	config := llvmTestOptions(t)
	sources := []string{
		mixedNumericLoop,
		`struct Box { let n:i64; } fn work(box:&Box, n:i64)->i64 { let i=0; while(i<n) { i+=1; } println(box.n); return i; } fn main() { println(work(&Box { n:42 },5)); }`,
		`fn main() { const xs=&List<i64>(); let i=0; while(i<5) { xs.push(i*2); i+=1; } println(xs.len()); }`,
		`struct Box { let n:i64; } fn main() { const box=&Box { n:42 }; let i=0; while(i<4) { i+=1; } println(box.n); println(9223372036854775807+1); }`,
		`fn main() { const xs=&List<i64>(7,9); let i=0; let sum=0; while(i<3) { sum+=match(xs.get(i)) { Some(n)=>n, None=>0 }; i+=1; } i=0; while(i<5) { i+=1; } println(sum); }`,
		`struct Box { let n:i64; } fn main() { let i=0; while(i<4) { if(true) { const b=&Box { n:i }; println(b.n); } i+=1; } println(i); }`,
	}
	for n, source := range sources {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p, err := Compile(source)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := preparePlan(p, false)
			if err != nil {
				t.Fatal(err)
			}
			_, _, mixed := emitLLVM(plan, p)
			if (n <= 1 || n == 3 || n == 4) && len(mixed) == 0 {
				t.Fatal("Expected a mixed native function")
			}
			j, err := CompileLLVM(p, config)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			for steps := 1; steps <= 250; steps++ {
				compareLLVM(t, p, j, Options{MaxSteps: steps})
			}
			for depth := 1; depth <= 4; depth++ {
				compareLLVM(t, p, j, Options{MaxDepth: depth})
			}
			var want, got []string
			wantErr := Execute(p, Options{Print: func(s string) { want = append(want, s) }})
			gotErr := j.Execute(Options{Print: func(s string) { runtime.GC(); got = append(got, s) }})
			if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !slices.Equal(want, got) {
				t.Fatalf("GC: %q / %v; expected %q / %v", got, gotErr, want, wantErr)
			}
		})
	}
	jitFrames.Range(func(_, _ any) bool { t.Error("Leaked mixed frame"); return true })
}

func BenchmarkLLVMMixedNumericLoop(b *testing.B) {
	p, err := Compile(mixedNumericLoop)
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
			options := Options{Print: func(string) {}}
			for n := 0; n < b.N; n++ {
				var err error
				if backend == "vm" {
					err = Execute(p, options)
				} else {
					err = j.Execute(options)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
