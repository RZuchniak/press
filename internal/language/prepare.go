package language

import (
	"sort"
)

func prepare(program *Program) (plan *executionPlan, err error) {
	return preparePlan(program, true)
}

func preparePlan(program *Program, optimize bool) (plan *executionPlan, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(languageError); ok {
				err = e
			} else {
				err = languageError{"Invalid bytecode during preparation"}
			}
		}
	}()
	validateBytecode(program)
	plan = &executionPlan{functions: map[string]*preparedFunction{}, layouts: map[string]*structLayout{}}
	for name, fn := range program.Functions {
		plan.functions[name] = &preparedFunction{name: name, params: len(fn.Params), receiver: fn.Receiver != "", result: fn.Result != "void", code: make([]preparedInstruction, len(fn.Code))}
	}
	for name, fields := range program.Structs {
		names := []string{}
		for field := range fields {
			names = append(names, field)
		}
		sort.Strings(names)
		layout := &structLayout{name: name, fields: map[string]int{}, methods: map[string]*preparedFunction{}}
		for i, field := range names {
			layout.fields[field] = i
		}
		for method, target := range program.Methods[name] {
			layout.methods[method] = plan.functions[target]
		}
		plan.layouts[name] = layout
	}
	for name, source := range program.Functions {
		fn := plan.functions[name]
		scopes := []map[string]int{{}}
		for _, parameter := range source.Params {
			scopes[0][parameter] = fn.locals
			fn.locals++
		}
		lookup := func(name string) int {
			for i := len(scopes) - 1; i >= 0; i-- {
				if slot, ok := scopes[i][name]; ok {
					return slot
				}
			}
			fail("Missing binding " + name)
			return 0
		}
		targets := map[int]bool{}
		for _, raw := range source.Code {
			switch raw[0].(string) {
			case "JUMP", "JUMP_FALSE", "AND", "OR":
				targets[operandInt(raw[1])] = true
			case "MATCH_VARIANT":
				targets[operandInt(raw[2])] = true
			}
		}
		for pc, raw := range source.Code {
			instruction := &fn.code[pc]
			instruction.span = 1
			switch op := raw[0].(string); op {
			case "COLLECTION":
				instruction.op = codeCollection
				instruction.collection = parseCollection(raw[1].(string))
				if instruction.collection == noCollection {
					fail("Invalid collection type")
				}
				instruction.count = operandInt(raw[2])
			case "CALL_COLLECTION":
				instruction.op = codeCallCollection
				instruction.collectionOp = resolveCollectionOp(raw[1].(string))
				instruction.count = operandInt(raw[2])
				instruction.hasResult = raw[3].(bool)
			case "PUSH":
				instruction.op = codePush
				switch raw[1].(string) {
				case "i64":
					instruction.constant = value{kind: intValue, number: parseInteger(raw[2].(string))}
				case "String":
					instruction.constant = value{kind: stringValue, text: raw[2].(string)}
				case "bool":
					instruction.constant = boolean(raw[2].(bool))
				default:
					fail("Invalid constant type")
				}
			case "LOAD":
				instruction.op = codeLoad
				instruction.slot = lookup(raw[1].(string))
			case "LOAD_PATH", "STORE":
				path := operandStrings(raw[1])
				if len(path) == 0 {
					fail("Empty field path")
				}
				instruction.slot = lookup(path[0])
				instruction.path = path[1:]
				if op == "LOAD_PATH" {
					instruction.op = codeLoadPath
				} else if len(path) == 1 {
					instruction.op = codeStoreLocal
				} else {
					instruction.op = codeStoreField
				}
			case "DECLARE":
				instruction.op = codeDeclare
				instruction.slot = fn.locals
				scopes[len(scopes)-1][raw[1].(string)] = fn.locals
				fn.locals++
			case "STRUCT":
				instruction.op = codeStruct
				instruction.layout = plan.layouts[raw[1].(string)]
				if instruction.layout == nil {
					fail("Missing struct layout")
				}
				for _, name := range operandStrings(raw[2]) {
					index, ok := instruction.layout.fields[name]
					if !ok {
						fail("Missing field " + name)
					}
					instruction.indices = append(instruction.indices, index)
				}
			case "REF":
				instruction.op = codeRef
			case "ENUM":
				instruction.op = codeEnum
				instruction.text = raw[2].(string)
				instruction.count = operandInt(raw[3])
			case "MATCH_VARIANT":
				instruction.op = codeMatch
				instruction.text = raw[1].(string)
				instruction.jump = operandInt(raw[2])
			case "PAYLOAD":
				instruction.op = codePayload
				instruction.count = operandInt(raw[1])
			case "TRY":
				instruction.op = codeTry
				instruction.text = raw[2].(string)
			case "TRAP":
				instruction.op = codeTrap
				instruction.text = raw[1].(string)
			case "FIELD":
				instruction.op = codeField
				instruction.path = []string{raw[1].(string)}
			case "UNARY":
				instruction.op = codeUnary
				instruction.text = raw[1].(string)
			case "BINARY":
				instruction.op = codeBinary
				operator, ok := arithmeticOps[raw[1].(string)]
				if !ok {
					fail("Unknown operator")
				}
				instruction.arithmetic = operator
			case "AND":
				instruction.op = codeAnd
				instruction.jump = operandInt(raw[1])
			case "OR":
				instruction.op = codeOr
				instruction.jump = operandInt(raw[1])
			case "PRINT":
				instruction.op = codePrint
			case "CALL":
				instruction.op = codeCall
				instruction.target = plan.functions[raw[1].(string)]
				if instruction.target == nil {
					fail("Missing function " + raw[1].(string))
				}
				instruction.count = operandInt(raw[2])
			case "CALL_METHOD":
				instruction.op = codeCallMethod
				instruction.text = raw[1].(string)
				instruction.count = operandInt(raw[2])
			case "RETURN":
				instruction.op = codeReturn
				if raw[1].(bool) {
					instruction.count = 1
				}
			case "POP":
				instruction.op = codePop
			case "ENTER":
				instruction.op = codeEnter
				scopes = append(scopes, map[string]int{})
			case "EXIT":
				instruction.op = codeExit
				if len(scopes) <= 1 {
					fail("Bytecode scope underflow")
				}
				for _, slot := range scopes[len(scopes)-1] {
					instruction.indices = append(instruction.indices, slot)
				}
				scopes = scopes[:len(scopes)-1]
			case "JUMP":
				instruction.op = codeJump
				instruction.jump = operandInt(raw[1])
			case "JUMP_FALSE":
				instruction.op = codeJumpFalse
				instruction.jump = operandInt(raw[1])
			default:
				fail("Unknown opcode " + op)
			}
		}
		if len(scopes) != 1 {
			fail("Unclosed bytecode scope in " + name)
		}
		for target := range targets {
			if target < 0 || target > len(fn.code) {
				fail("Invalid bytecode jump")
			}
		}
		if optimize {
			optimizeFunction(fn, targets)
		}
	}
	return plan, nil
}
