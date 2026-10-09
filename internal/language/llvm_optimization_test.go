package language

import (
	"fmt"
	"press/internal/bench"
	"runtime"
	"slices"
	"testing"
)

func TestLLVMOptimizationValidation(t *testing.T) {
	if _, err := CompileLLVM(nil, LLVMOptions{Optimization: "O9"}); err == nil {
		t.Fatal("Accepted unknown optimization")
	}
}

func TestLLVMOptimizationModes(t *testing.T) {
	config := llvmTestOptions(t)
	sources := []string{mixedNumericLoop,
		`fn main(){ println(9223372036854775807+1); }`,
		`fn main(){println(false && (1/0==1)); println(-9223372036854775808 % -1);}`,
		`fn recur(n:i64)->i64 { if(n==0){return 1;} return n*recur(n-1); } fn main(){println(recur(8));}`,
	}
	for _, w := range bench.Workloads() {
		sources = append(sources, w.Source)
	}
	for _, mode := range []LLVMOptimization{LLVMOptNone, LLVMOptO1, LLVMOptO2} {
		t.Run(string(mode), func(t *testing.T) {
			config.Optimization = mode
			for n, source := range sources {
				t.Run(fmt.Sprint(n), func(t *testing.T) {
					p, err := Compile(source)
					if err != nil {
						t.Fatal(err)
					}
					j, err := CompileLLVM(p, config)
					if err != nil {
						t.Fatal(err)
					}
					defer j.Close()
					for steps := 1; steps <= 150; steps++ {
						compareLLVM(t, p, j, Options{MaxSteps: steps})
					}
					for depth := 1; depth <= 8; depth++ {
						compareLLVM(t, p, j, Options{MaxDepth: depth})
					}
					var want, got []string
					wantErr := Execute(p, Options{Print: func(s string) { want = append(want, s) }})
					gotErr := j.Execute(Options{Print: func(s string) { runtime.GC(); got = append(got, s) }})
					if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !slices.Equal(want, got) {
						t.Fatalf("GC: %q / %v, expected %q / %v", got, gotErr, want, wantErr)
					}
				})
			}
		})
	}
}

func BenchmarkLLVMOptimizationLevels(b *testing.B) {
	workloads := []bench.Workload{{Name: "mixed_loop", Source: mixedNumericLoop}}
	for _, w := range bench.Workloads() {
		if w.Name == "arithmetic" || w.Name == "function_calls" || w.Name == "list_operations" || w.Name == "shared_references" {
			workloads = append(workloads, w)
		}
	}
	config := llvmTestOptions(b)
	for _, w := range workloads {
		b.Run(w.Name, func(b *testing.B) {
			p, err := Compile(w.Source)
			if err != nil {
				b.Fatal(err)
			}
			for _, mode := range []LLVMOptimization{LLVMOptNone, LLVMOptO1, LLVMOptO2} {
				b.Run(string(mode), func(b *testing.B) {
					config.Optimization = mode
					j, err := CompileLLVM(p, config)
					if err != nil {
						b.Fatal(err)
					}
					defer j.Close()
					options := Options{Print: func(string) {}}
					b.Run("execute", func(b *testing.B) {
						b.ReportAllocs()
						for n := 0; n < b.N; n++ {
							if err := j.Execute(options); err != nil {
								b.Fatal(err)
							}
						}
					})
					b.Run("compile_close", func(b *testing.B) {
						b.ReportAllocs()
						for n := 0; n < b.N; n++ {
							compiled, err := CompileLLVM(p, config)
							if err != nil {
								b.Fatal(err)
							}
							if err := compiled.Close(); err != nil {
								b.Fatal(err)
							}
						}
					})
				})
			}
		})
	}
}
