package language

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// Shared targets are ordinary Go pointers, visible to Go's collector.
type structValue struct {
	Type   string
	Fields map[string]any
}
type reference struct{ Target structValue }
type enumValue struct {
	Type, Variant string
	Payload       []any
}

func copyValue(value any) any {
	switch value := value.(type) {
	case structValue:
		fields := make(map[string]any, len(value.Fields))
		for name, v := range value.Fields {
			fields[name] = copyValue(v)
		}
		return structValue{value.Type, fields}
	case enumValue:
		return enumValue{value.Type, value.Variant, copyValues(value.Payload)}
	default:
		return value
	}
}
func copyValues(values []any) []any {
	copied := make([]any, len(values))
	for i, v := range values {
		copied[i] = copyValue(v)
	}
	return copied
}
func object(value any) structValue {
	switch v := value.(type) {
	case structValue:
		return v
	case *reference:
		return v.Target
	}
	fail("Expected struct object")
	return structValue{}
}
func operandInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		if v >= 0 && v <= math.MaxInt32 && v == math.Trunc(v) {
			return int(v)
		}
	}
	fail("Invalid instruction operand")
	return 0
}
func operandStrings(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, len(v))
		for i, s := range v {
			result[i] = s.(string)
		}
		return result
	}
	fail("Invalid string-list operand")
	return nil
}
func arithmetic(op string, a, b any) any {
	if op == "==" {
		return a == b
	}
	if op == "!=" {
		return a != b
	}
	if s, ok := a.(string); ok && op == "+" {
		return s + b.(string)
	}
	left, right := a.(int64), b.(int64)
	var result int64
	switch op {
	case "+":
		result = left + right
		if (right > 0 && result < left) || (right < 0 && result > left) {
			fail("i64 overflow")
		}
	case "-":
		result = left - right
		if (right < 0 && result < left) || (right > 0 && result > left) {
			fail("i64 overflow")
		}
	case "*":
		if (left == math.MinInt64 && right == -1) || (right == math.MinInt64 && left == -1) {
			fail("i64 overflow")
		}
		result = left * right
		if right != 0 && result/right != left {
			fail("i64 overflow")
		}
	case "/":
		if right == 0 {
			fail("Division by zero")
		}
		if left == math.MinInt64 && right == -1 {
			fail("i64 overflow")
		}
		result = left / right
	case "%":
		if right == 0 {
			fail("Division by zero")
		}
		result = left % right
	case "<":
		return left < right
	case ">":
		return left > right
	case "<=":
		return left <= right
	case ">=":
		return left >= right
	default:
		fail("Unknown operator " + op)
	}
	return result
}

type Options struct {
	Print              func(string)
	MaxSteps, MaxDepth int
}
type machine struct {
	program      *Program
	options      Options
	steps, depth int
}

// Execute runs trusted compiler output. It is not a sandbox or bytecode verifier.
func Execute(program *Program, options Options) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(languageError); ok {
				err = e
			} else {
				err = fmt.Errorf("Invalid bytecode: %v", r)
			}
		}
	}()
	if program == nil || program.Format != "press-bytecode" || (program.Version != 1 && program.Version != 2 && program.Version != 3) || program.Functions["main"] == nil {
		fail("Unsupported bytecode format/version")
	}
	if options.Print == nil {
		options.Print = func(s string) { fmt.Println(s) }
	}
	if options.MaxSteps <= 0 {
		options.MaxSteps = 1_000_000
	}
	if options.MaxDepth <= 0 {
		options.MaxDepth = 256
	}
	vm := machine{program: program, options: options}
	vm.run("main", nil)
	return nil
}
func Decode(reader io.Reader) (*Program, error) {
	decoder := json.NewDecoder(reader)
	var program Program
	if err := decoder.Decode(&program); err != nil {
		return nil, fmt.Errorf("Invalid bytecode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("Unexpected data after bytecode artifact")
	}
	return &program, nil
}
func (vm *machine) run(name string, args []any) any {
	vm.depth++
	defer func() { vm.depth-- }()
	if vm.depth > vm.options.MaxDepth {
		fail("Call depth limit exceeded")
	}
	fn := vm.program.Functions[name]
	if fn == nil {
		fail("Missing function " + name)
	}
	if len(args) != len(fn.Params) {
		fail("Wrong bytecode argument count for " + name)
	}
	scopes := []map[string]any{{}}
	for i, name := range fn.Params {
		if i == 0 && fn.Receiver != "" {
			scopes[0][name] = object(args[i])
		} else {
			scopes[0][name] = copyValue(args[i])
		}
	}
	stack := []any{}
	pop := func() any {
		if len(stack) == 0 {
			fail("Bytecode stack underflow")
		}
		value := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return value
	}
	top := func() any {
		if len(stack) == 0 {
			fail("Bytecode stack underflow")
		}
		return stack[len(stack)-1]
	}
	popMany := func(count int) []any {
		if count < 0 || count > len(stack) {
			fail("Bytecode stack underflow")
		}
		values := append([]any{}, stack[len(stack)-count:]...)
		stack = stack[:len(stack)-count]
		return values
	}
	binding := func(name string) map[string]any {
		for i := len(scopes) - 1; i >= 0; i-- {
			if _, ok := scopes[i][name]; ok {
				return scopes[i]
			}
		}
		fail("Missing binding " + name)
		return nil
	}
	pc := 0
	for pc < len(fn.Code) {
		vm.steps++
		if vm.steps > vm.options.MaxSteps {
			fail("Instruction limit exceeded")
		}
		instruction := fn.Code[pc]
		pc++
		op := instruction[0].(string)
		switch op {
		case "PUSH":
			if instruction[1] == "i64" {
				stack = append(stack, parseInteger(instruction[2].(string)))
			} else {
				stack = append(stack, instruction[2])
			}
		case "LOAD":
			name := instruction[1].(string)
			stack = append(stack, copyValue(binding(name)[name]))
		case "LOAD_PATH":
			path := operandStrings(instruction[1])
			value := binding(path[0])[path[0]]
			for _, key := range path[1:] {
				value = object(value).Fields[key]
			}
			stack = append(stack, value)
		case "DECLARE":
			scopes[len(scopes)-1][instruction[1].(string)] = copyValue(pop())
		case "STORE":
			path := operandStrings(instruction[1])
			value := copyValue(pop())
			scope := binding(path[0])
			if len(path) == 1 {
				scope[path[0]] = value
			} else {
				parent := scope[path[0]]
				for _, key := range path[1 : len(path)-1] {
					parent = object(parent).Fields[key]
				}
				object(parent).Fields[path[len(path)-1]] = value
			}
		case "STRUCT":
			names := operandStrings(instruction[2])
			values := popMany(len(names))
			fields := make(map[string]any, len(names))
			for i, name := range names {
				fields[name] = copyValue(values[i])
			}
			stack = append(stack, structValue{instruction[1].(string), fields})
		case "REF":
			stack = append(stack, &reference{pop().(structValue)})
		case "ENUM":
			values := popMany(operandInt(instruction[3]))
			stack = append(stack, enumValue{instruction[1].(string), instruction[2].(string), copyValues(values)})
		case "MATCH_VARIANT":
			if top().(enumValue).Variant != instruction[1].(string) {
				pc = operandInt(instruction[2])
			}
		case "PAYLOAD":
			stack = append(stack, copyValue(top().(enumValue).Payload[operandInt(instruction[1])]))
		case "TRY":
			value := pop().(enumValue)
			if value.Variant != instruction[2].(string) {
				value.Type = instruction[1].(string)
				return copyValue(value)
			}
			stack = append(stack, copyValue(value.Payload[0]))
		case "TRAP":
			fail(instruction[1].(string))
		case "FIELD":
			stack = append(stack, copyValue(object(pop()).Fields[instruction[1].(string)]))
		case "UNARY":
			value := pop()
			if instruction[1] == "!" {
				stack = append(stack, !value.(bool))
			} else {
				n := value.(int64)
				if n == math.MinInt64 {
					fail("i64 overflow")
				}
				stack = append(stack, -n)
			}
		case "BINARY":
			right, left := pop(), pop()
			stack = append(stack, arithmetic(instruction[1].(string), left, right))
		case "AND":
			if !top().(bool) {
				pc = operandInt(instruction[1])
			} else {
				pop()
			}
		case "OR":
			if top().(bool) {
				pc = operandInt(instruction[1])
			} else {
				pop()
			}
		case "PRINT":
			vm.options.Print(fmt.Sprint(pop()))
		case "CALL":
			name := instruction[1].(string)
			result := vm.run(name, popMany(operandInt(instruction[2])))
			if vm.program.Functions[name].Result != "void" {
				stack = append(stack, result)
			}
		case "CALL_METHOD":
			arguments := popMany(operandInt(instruction[2]))
			receiver := pop()
			target := object(receiver)
			key := vm.program.Methods[target.Type][instruction[1].(string)]
			if key == "" {
				fail("Missing method dispatch target")
			}
			result := vm.run(key, append([]any{receiver}, arguments...))
			if vm.program.Functions[key].Result != "void" {
				stack = append(stack, result)
			}
		case "RETURN":
			if instruction[1].(bool) {
				return copyValue(pop())
			}
			return nil
		case "POP":
			pop()
		case "ENTER":
			scopes = append(scopes, map[string]any{})
		case "EXIT":
			if len(scopes) <= 1 {
				fail("Bytecode scope underflow")
			}
			scopes = scopes[:len(scopes)-1]
		case "JUMP":
			pc = operandInt(instruction[1])
		case "JUMP_FALSE":
			if !pop().(bool) {
				pc = operandInt(instruction[1])
			}
		default:
			fail("Unknown opcode " + op)
		}
		if pc < 0 || pc > len(fn.Code) {
			fail("Invalid bytecode jump")
		}
	}
	fail("Function " + name + " fell through")
	return nil
}
