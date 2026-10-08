//go:build !windows || !amd64

package language

import "fmt"

func newLLVMNative(string, map[*preparedFunction]string, map[*preparedFunction]*mixedAnalysis, LLVMOptions) (nativeExecutor, error) {
	return nil, fmt.Errorf("LLVM execution currently requires Windows/amd64; LLVMIR and the VM remain available")
}
