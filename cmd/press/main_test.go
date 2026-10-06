package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLICompileExecuteCheck(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "example.press")
	artifact := filepath.Join(directory, "example.pbc")
	if err := os.WriteFile(source, []byte("fn main() { println(42); }"), 0644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"compile", source, artifact}, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("Compile printed output")
	}
	// Execute with the source deleted: this is a standalone bytecode artifact.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"exec", artifact}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "42\n" {
		t.Fatal(output.String())
	}
	if err := os.WriteFile(source, []byte("fn main() { println(42); }"), 0644); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := run([]string{"check", source}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Type check passed.\n" {
		t.Fatal(output.String())
	}
	output.Reset()
	if err := run([]string{"run", source}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "42\n" {
		t.Fatal(output.String())
	}
}
func TestCLIErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"bad", "file"}, {"compile", "file"}, {"run", "file", "extra"}, {"exec", "missing-file"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("Accepted arguments %q", args)
		}
	}
	file := filepath.Join(t.TempDir(), "invalid.press")
	if err := os.WriteFile(file, []byte("fn main() { const n = 1; n = 2; }"), 0644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"check", file}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Cannot assign") {
		t.Fatal(err)
	}
}

func TestCLIImports(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "main.press")
	module := filepath.Join(dir, "store.press")
	artifact := filepath.Join(dir, "tasks.pbc")
	if err := os.WriteFile(root, []byte(`import "store.press"; fn main() { println(answer()); }`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte(`fn answer() -> i64 { const values = &Map<String,i64>(); values.set("answer",42); return match (values.get("answer")) { Some(n) => n, None => 0 }; }`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	for _, command := range []string{"run", "check"} {
		output.Reset()
		if err := run([]string{command, root}, &output); err != nil {
			t.Fatal(err)
		}
		expected := "42\n"
		if command == "check" {
			expected = "Type check passed.\n"
		}
		if output.String() != expected {
			t.Fatal(output.String())
		}
	}
	output.Reset()
	if err := run([]string{"compile", root, artifact}, &output); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, module} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"exec", artifact}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "42\n" {
		t.Fatal(output.String())
	}
}
