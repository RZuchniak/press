package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"press/internal/language"
)

func run(args []string, stdout io.Writer) error {
	usage := "Usage: press <run|run-llvm|check> source.press\n       press <compile|llvm-ir> source.press output\n       press <exec|exec-llvm> output.pbc"
	if len(args) < 2 {
		return fmt.Errorf("%s", usage)
	}
	command := args[0]
	writesOutput := command == "compile" || command == "llvm-ir"
	if (writesOutput && len(args) != 3) || (!writesOutput && len(args) != 2) {
		return fmt.Errorf("%s", usage)
	}
	if command != "run" && command != "check" && command != "compile" && command != "exec" && command != "run-llvm" && command != "exec-llvm" && command != "llvm-ir" {
		return fmt.Errorf("%s", usage)
	}
	options := language.Options{Print: func(s string) { fmt.Fprintln(stdout, s) }}
	execute := func(program *language.Program) error {
		if command != "run-llvm" && command != "exec-llvm" {
			return language.Execute(program, options)
		}
		compiled, err := language.CompileLLVM(program, language.LLVMOptions{Optimization: language.LLVMOptimization(os.Getenv("PRESS_LLVM_OPT"))})
		if err != nil {
			return err
		}
		runErr := compiled.Execute(options)
		closeErr := compiled.Close()
		if runErr != nil {
			return runErr
		}
		return closeErr
	}
	if command == "exec" || command == "exec-llvm" {
		file, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer file.Close()
		program, err := language.Decode(file)
		if err != nil {
			return err
		}
		return execute(program)
	}
	program, err := language.CompileFile(args[1])
	if err != nil {
		return err
	}
	switch command {
	case "run", "run-llvm":
		return execute(program)
	case "llvm-ir":
		ir, err := language.LLVMIR(program)
		if err != nil {
			return err
		}
		return os.WriteFile(args[2], []byte(ir), 0644)
	case "check":
		fmt.Fprintln(stdout, "Type check passed.")
		return nil
	case "compile":
		bytes, err := json.MarshalIndent(program, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(args[2], append(bytes, '\n'), 0644)
	}
	return nil
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
