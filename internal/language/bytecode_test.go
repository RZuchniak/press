package language

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMalformedInstructionsRejectedBeforeExecution(t *testing.T) {
	tests := []struct {
		name        string
		instruction Instruction
		expected    string
	}{
		{"missing operand", Instruction{"PUSH", "i64"}, "Wrong operand count"},
		{"extra operand", Instruction{"PRINT", 1}, "Wrong operand count"},
		{"nonstring opcode", Instruction{true}, "Opcode must be a string"},
		{"bad literal", Instruction{"PUSH", "bool", "false"}, "boolean literal"},
		{"bad constant type", Instruction{"PUSH", "List<i64>", 1}, "Invalid constant type"},
		{"numeric i64 literal", Instruction{"PUSH", "i64", 9007199254740993.0}, "decimal string"},
		{"negative count", Instruction{"ENUM", "Option<i64>", "Some", -1}, "Invalid instruction operand"},
		{"fractional count", Instruction{"PAYLOAD", 0.5}, "Invalid instruction operand"},
		{"bad field path", Instruction{"STORE", []any{"local", false}}, "string-list operand"},
		{"missing call", Instruction{"CALL", "missing", 0}, "Missing call target"},
		{"bad unary operator", Instruction{"UNARY", "~"}, "Unknown unary operator"},
		{"bad binary operator", Instruction{"BINARY", "**"}, "Unknown binary operator"},
		{"bad collection", Instruction{"COLLECTION", "Set", 0}, "Invalid collection type"},
		{"map initializer", Instruction{"COLLECTION", "Map", 1}, "no arguments"},
		{"bad collection method", Instruction{"CALL_COLLECTION", "sort", 0, true}, "Unknown collection operation"},
		{"wrong collection arity", Instruction{"CALL_COLLECTION", "get", 0, true}, "call contract"},
		{"wrong result flag", Instruction{"CALL_COLLECTION", "push", 1, true}, "call contract"},
		{"bad jump", Instruction{"JUMP", 10000}, "Invalid bytecode jump"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, roundTrip := range []bool{false, true} {
				program := &Program{Format: "press-bytecode", Version: 4, Functions: map[string]*Function{
					"main": {Result: "void", Code: []Instruction{{"PUSH", "String", "side effect"}, {"PRINT"}, test.instruction, {"RETURN", false}}},
				}}
				if roundTrip {
					data, err := json.Marshal(program)
					if err != nil {
						t.Fatal(err)
					}
					program, err = Decode(bytes.NewReader(data))
					if err != nil {
						t.Fatal(err)
					}
				}
				printed := false
				err := Execute(program, Options{Print: func(string) { printed = true }})
				if err == nil || !strings.Contains(err.Error(), test.expected) || !strings.Contains(err.Error(), "main at instruction 2") {
					t.Fatalf("roundTrip=%v: %v", roundTrip, err)
				}
				if printed {
					t.Fatal("Guest code executed before validation")
				}
			}
		})
	}
}

func TestBytecodeMetadataAndLinking(t *testing.T) {
	tests := []struct {
		source   string
		change   func(*Program)
		expected string
	}{
		{`fn main() {}`, func(p *Program) { p.Functions["unused"] = nil }, "nil function"},
		{`fn f(n: i64) {} fn main() { f(1); }`, func(p *Program) { p.Functions["f"].Params = append(p.Functions["f"].Params, "n") }, "parameter metadata"},
		{`fn f(n: i64) {} fn main() { f(1); }`, func(p *Program) { p.Functions["f"].Params = nil; p.Functions["f"].ParamTypes = nil }, "Wrong argument count"},
		{`struct S { let n: i64; } fn main() { let s = S { n: 1 }; }`, func(p *Program) { p.Functions["main"].Code[1][2] = []string{} }, "Incomplete struct fields"},
		{`struct S { let n: i64; } fn main() { let s = S { n: 1 }; }`, func(p *Program) { p.Functions["main"].Code[1][2] = []string{"n", "n"} }, "duplicate struct field"},
		{`struct S { fn f() {} } fn main() {}`, func(p *Program) { p.Methods["S"]["f"] = "missing" }, "Invalid method target"},
		{`fn main() { let list = List<i64>(); }`, func(p *Program) { p.Version = 3 }, "version 4"},
		{`fn main() {}`, func(p *Program) { p.Functions["main"].Code = []Instruction{{"ENTER"}, {"RETURN", false}} }, "Unclosed bytecode scope"},
	}
	for _, test := range tests {
		program, err := Compile(test.source)
		if err != nil {
			t.Fatal(err)
		}
		test.change(program)
		err = Execute(program, Options{Print: func(string) { t.Fatal("Unexpected output") }})
		if err == nil || !strings.Contains(err.Error(), test.expected) {
			t.Fatalf("%s: %v", test.expected, err)
		}
	}
}
