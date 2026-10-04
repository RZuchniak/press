package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"press/internal/language"
)

func run(args []string, stdout io.Writer) error {
	usage := "Usage: press <run|check> source.press\n       press compile source.press output.pbc\n       press exec output.pbc"
	if len(args) < 2 {
		return fmt.Errorf("%s", usage)
	}
	command := args[0]
	if (command == "compile" && len(args) != 3) || (command != "compile" && len(args) != 2) {
		return fmt.Errorf("%s", usage)
	}
	if command != "run" && command != "check" && command != "compile" && command != "exec" {
		return fmt.Errorf("%s", usage)
	}
	options := language.Options{Print: func(s string) { fmt.Fprintln(stdout, s) }}
	if command == "exec" {
		file, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer file.Close()
		program, err := language.Decode(file)
		if err != nil {
			return err
		}
		return language.Execute(program, options)
	}
	source, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	program, err := language.Compile(string(source))
	if err != nil {
		return err
	}
	switch command {
	case "run":
		return language.Execute(program, options)
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
