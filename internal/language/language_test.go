package language

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// Captured before removing the Node implementation. These fixtures preserve its
// successful output, rejected source programs and runtime errors without Node.
func TestMigrationRegressions(t *testing.T) {
	var cases []struct {
		Source       string `json:"source"`
		CompileError string `json:"compileError"`
		Executions   []struct {
			Output       []string `json:"output"`
			RuntimeError string   `json:"runtimeError"`
			MaxSteps     int      `json:"maxSteps"`
		} `json:"executions"`
	}
	data, err := os.ReadFile("testdata/regressions.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 50 {
		t.Fatalf("Incomplete migration fixtures: %d", len(cases))
	}
	for i, testCase := range cases {
		t.Run(fmt.Sprintf("case_%02d", i+1), func(t *testing.T) {
			program, err := Compile(testCase.Source)
			if testCase.CompileError != "" {
				expected := testCase.CompileError
				if strings.Contains(expected, "Recursive value layout") {
					expected = "Recursive value layout"
				}
				if err == nil || !strings.Contains(err.Error(), expected) {
					t.Fatalf("Expected compile error %q, got %v", expected, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(program)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			for _, execution := range testCase.Executions {
				output := []string{}
				err := Execute(decoded, Options{Print: func(s string) { output = append(output, s) }, MaxSteps: execution.MaxSteps})
				if execution.RuntimeError != "" {
					if err == nil || !strings.Contains(err.Error(), execution.RuntimeError) {
						t.Fatalf("Expected runtime error %q, got %v", execution.RuntimeError, err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(output, execution.Output) {
					t.Fatalf("Output %q; expected %q", output, execution.Output)
				}
			}
		})
	}
}

func TestOriginalBytecodeCompatibility(t *testing.T) {
	for _, tc := range []struct {
		Name   string
		Output []string
	}{
		{"node-counter.json", []string{"1", "0", "0", "2", "4", "Shared mutation works."}},
		{"node-results.json", []string{"Alice", "Anonymous", "Invalid user ID", "Database unavailable"}},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			file, err := os.Open("testdata/" + tc.Name)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			program, err := Decode(file)
			if err != nil {
				t.Fatal(err)
			}
			output := []string{}
			if err := Execute(program, Options{Print: func(s string) { output = append(output, s) }}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(output, tc.Output) {
				t.Fatalf("Output %q", output)
			}
		})
	}
}
func TestIntegerBoundaries(t *testing.T) {
	cases := []struct{ Expression, Output, Error string }{
		{"9223372036854775807 + 1", "", "i64 overflow"},
		{"-9223372036854775808 - 1", "", "i64 overflow"},
		{"-9223372036854775808 * -1", "", "i64 overflow"},
		{"-1 * -9223372036854775808", "", "i64 overflow"},
		{"3037000500 * 3037000500", "", "i64 overflow"},
		{"-9223372036854775808 / -1", "", "i64 overflow"},
		{"-9223372036854775808 % -1", "0", ""},
		{"-(-9223372036854775808)", "", "i64 overflow"},
		{"-9223372036854775808 + 1", "-9223372036854775807", ""},
		{"0 * -9223372036854775808", "0", ""},
		{"-7 / 2", "-3", ""},
		{"-7 % 2", "-1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.Expression, func(t *testing.T) {
			p, err := Compile("fn main() { println(" + tc.Expression + "); }")
			if err != nil {
				t.Fatal(err)
			}
			output := ""
			err = Execute(p, Options{Print: func(s string) { output = s }})
			if tc.Error != "" {
				if err == nil || err.Error() != tc.Error {
					t.Fatalf("Expected %s, got %v", tc.Error, err)
				}
			} else if err != nil || output != tc.Output {
				t.Fatalf("Output %q, error %v", output, err)
			}
		})
	}
}
func TestLimitsAndMalformedArtifacts(t *testing.T) {
	recursive, err := Compile("fn recurse() { recurse(); } fn main() { recurse(); }")
	if err != nil {
		t.Fatal(err)
	}
	if err = Execute(recursive, Options{MaxDepth: 10}); err == nil || !strings.Contains(err.Error(), "Call depth") {
		t.Fatal(err)
	}
	for _, raw := range []string{"{", "{} {}"} {
		if _, err := Decode(strings.NewReader(raw)); err == nil {
			t.Fatalf("Accepted malformed JSON %s", raw)
		}
	}
	for _, code := range [][]Instruction{
		{{"POP"}}, {{"UNKNOWN"}}, {{"JUMP", -1}}, {{"LOAD", "missing"}}, {{"EXIT"}}, {{"PUSH", "i64", "9223372036854775808"}},
	} {
		program := &Program{Format: "press-bytecode", Version: 2, Functions: map[string]*Function{"main": {Result: "void", Code: code}}}
		if err := Execute(program, Options{}); err == nil {
			t.Fatalf("Accepted malformed code %v", code)
		}
	}
	for _, version := range []int{1, 2, 3, 99} {
		program := &Program{Format: "press-bytecode", Version: version, Functions: map[string]*Function{"main": {Result: "void", Code: []Instruction{{"RETURN", false}}}}}
		err := Execute(program, Options{})
		if (version == 99) != (err != nil) {
			t.Fatalf("Version %d: %v", version, err)
		}
	}
}
