package language

import (
	"fmt"
	"sort"
)

type operandKind uint8

const (
	stringOperand operandKind = iota
	integerOperand
	stringsOperand
	booleanOperand
	literalOperand
)

// This is the portable instruction contract, not the internal optimized plan.
var instructionOperands = map[string][]operandKind{
	"PUSH": {stringOperand, literalOperand},
	"LOAD": {stringOperand}, "DECLARE": {stringOperand},
	"LOAD_PATH": {stringsOperand}, "STORE": {stringsOperand},
	"STRUCT": {stringOperand, stringsOperand}, "REF": {},
	"ENUM":          {stringOperand, stringOperand, integerOperand},
	"MATCH_VARIANT": {stringOperand, integerOperand},
	"PAYLOAD":       {integerOperand}, "TRY": {stringOperand, stringOperand},
	"TRAP": {stringOperand}, "FIELD": {stringOperand},
	"UNARY": {stringOperand}, "BINARY": {stringOperand},
	"AND": {integerOperand}, "OR": {integerOperand},
	"PRINT": {}, "CALL": {stringOperand, integerOperand},
	"CALL_METHOD": {stringOperand, integerOperand},
	"RETURN":      {booleanOperand}, "POP": {}, "ENTER": {}, "EXIT": {},
	"JUMP": {integerOperand}, "JUMP_FALSE": {integerOperand},
	"COLLECTION":      {stringOperand, integerOperand},
	"CALL_COLLECTION": {stringOperand, integerOperand, booleanOperand},
}

func validateHeader(program *Program) {
	if program == nil || program.Format != "press-bytecode" || program.Version < 1 || program.Version > 4 || program.Functions["main"] == nil {
		fail("Unsupported bytecode format/version")
	}
}

// Validate representation and linking before executing any guest instructions.
// Stack-flow and semantic type verification are deliberately separate future work.
func validateBytecode(program *Program) {
	validateHeader(program)
	names := make([]string, 0, len(program.Functions))
	for name := range program.Functions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fn := program.Functions[name]
		if fn == nil {
			fail("Invalid bytecode: nil function " + name)
		}
		if len(fn.ParamTypes) != 0 && len(fn.ParamTypes) != len(fn.Params) {
			fail("Invalid parameter metadata for " + name)
		}
		seen := map[string]bool{}
		for _, parameter := range fn.Params {
			if parameter == "" || seen[parameter] {
				fail("Invalid/duplicate parameter in " + name)
			}
			seen[parameter] = true
		}
		for pc, raw := range fn.Code {
			validateInstruction(program, name, pc, raw)
		}
	}
	for owner, methods := range program.Methods {
		if program.Structs[owner] == nil {
			fail("Missing method owner " + owner)
		}
		for method, target := range methods {
			fn := program.Functions[target]
			if fn == nil || fn.Receiver != owner || len(fn.Params) == 0 {
				fail("Invalid method target for " + owner + "." + method)
			}
		}
	}
}

func validateInstruction(program *Program, function string, pc int, raw Instruction) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(languageError); ok {
				panic(languageError{fmt.Sprintf("Invalid bytecode in %s at instruction %d: %s", function, pc, e.message)})
			}
			panic(r)
		}
	}()
	if len(raw) == 0 {
		fail("Empty instruction")
	}
	op, ok := raw[0].(string)
	if !ok {
		fail("Opcode must be a string")
	}
	operands, ok := instructionOperands[op]
	if !ok {
		fail("Unknown opcode " + op)
	}
	if len(raw) != len(operands)+1 {
		fail("Wrong operand count for " + op)
	}
	for i, kind := range operands {
		operand := raw[i+1]
		switch kind {
		case stringOperand:
			if _, ok := operand.(string); !ok {
				fail("Expected string operand")
			}
		case integerOperand:
			operandInt(operand)
		case stringsOperand:
			operandStrings(operand)
		case booleanOperand:
			if _, ok := operand.(bool); !ok {
				fail("Expected boolean operand")
			}
		case literalOperand:
			switch raw[1].(string) {
			case "i64":
				text, ok := operand.(string)
				if !ok {
					fail("i64 literal must be a decimal string")
				}
				parseInteger(text)
			case "String":
				if _, ok := operand.(string); !ok {
					fail("Expected string literal")
				}
			case "bool":
				if _, ok := operand.(bool); !ok {
					fail("Expected boolean literal")
				}
			default:
				fail("Invalid constant type")
			}
		}
	}
	switch op {
	case "COLLECTION", "CALL_COLLECTION":
		if program.Version < 4 {
			fail("Collection instructions require bytecode version 4")
		}
		if op == "COLLECTION" {
			kind := parseCollection(raw[1].(string))
			if kind == noCollection {
				fail("Invalid collection type")
			}
			if kind == mapCollection && operandInt(raw[2]) != 0 {
				fail("Map constructor takes no arguments")
			}
		} else {
			operation := resolveCollectionOp(raw[1].(string))
			if operation == invalidCollectionOp {
				fail("Unknown collection operation")
			}
			contract := collectionContracts[operation]
			if operandInt(raw[2]) != contract.arguments || raw[3].(bool) != contract.returns {
				fail("Invalid collection call contract")
			}
		}
	case "CALL":
		target := program.Functions[raw[1].(string)]
		if target == nil {
			fail("Missing call target")
		}
		if operandInt(raw[2]) != len(target.Params) {
			fail("Wrong argument count for call")
		}
	case "STRUCT":
		fields := program.Structs[raw[1].(string)]
		if fields == nil {
			fail("Missing struct layout")
		}
		seen := map[string]bool{}
		for _, field := range operandStrings(raw[2]) {
			if _, ok := fields[field]; !ok || seen[field] {
				fail("Invalid/duplicate struct field")
			}
			seen[field] = true
		}
		if len(seen) != len(fields) {
			fail("Incomplete struct fields")
		}
	case "UNARY":
		if raw[1] != "!" && raw[1] != "-" {
			fail("Unknown unary operator")
		}
	case "BINARY":
		if _, ok := arithmeticOps[raw[1].(string)]; !ok {
			fail("Unknown binary operator")
		}
	case "JUMP", "JUMP_FALSE", "AND", "OR":
		if operandInt(raw[1]) > len(program.Functions[function].Code) {
			fail("Invalid bytecode jump")
		}
	case "MATCH_VARIANT":
		if operandInt(raw[2]) > len(program.Functions[function].Code) {
			fail("Invalid bytecode jump")
		}
	}
}
