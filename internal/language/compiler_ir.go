package language

import "strconv"

// Compiler instructions are independent of both JSON arrays and optimized VM
// instructions. Branch destinations use instruction indices until serialization.
type irOpcode uint8

const (
	irLoad irOpcode = iota
	irDeclare
	irLoadPath
	irStore
	irRef
	irMatch
	irTrap
	irField
	irAnd
	irOr
	irPrint
	irCall
	irCallMethod
	irPop
	irEnter
	irExit
	irJump
	irJumpFalse
)

type constantKind uint8

const (
	integerConstant constantKind = iota
	stringConstant
	booleanConstant
)

type constantInstruction struct {
	kind    constantKind
	number  int64
	text    string
	boolean bool
}

// Each compiler instruction stores only its operand category. The unexported
// serialization method closes the instruction set to this package.
type compilerInstruction interface{ serialize() Instruction }
type namedInstruction struct {
	op   irOpcode
	name string
}
type pathInstruction struct {
	op    irOpcode
	names []string
}
type structInstruction struct {
	typ   *typeRef
	names []string
}
type enumInstruction struct {
	typ   *typeRef
	name  string
	count int
}
type branchInstruction struct {
	op          irOpcode
	name        string
	destination int
}
type tryInstruction struct {
	typ     *typeRef
	success string
}
type callInstruction struct {
	op    irOpcode
	name  string
	count int
}
type collectionInstruction struct {
	kind  collectionKind
	count int
}
type collectionCallInstruction collectionOp
type simpleInstruction irOpcode
type payloadInstruction int
type unaryInstruction bool
type binaryInstruction arithmeticOp
type returnInstruction bool

type compilerFunction struct {
	params           []string
	paramTypes       []*typeRef
	result, receiver *typeRef
	code             []compilerInstruction
}

func (f *functionCompiler) emit(instruction compilerInstruction) int {
	f.fn.code = append(f.fn.code, instruction)
	return len(f.fn.code) - 1
}

func (f *functionCompiler) emitSimple(op irOpcode) int { return f.emit(simpleInstruction(op)) }
func (f *functionCompiler) emitNamed(op irOpcode, name string) int {
	return f.emit(namedInstruction{op: op, name: name})
}
func (f *functionCompiler) emitJump(op irOpcode, destination int) int {
	return f.emit(&branchInstruction{op: op, destination: destination})
}
func (f *functionCompiler) patchJump(index, destination int) {
	branch, ok := f.fn.code[index].(*branchInstruction)
	if !ok {
		panic("Compiler attempted to patch a non-branch instruction")
	}
	branch.destination = destination
}
func (f *functionCompiler) emitLiteral(n *Node) {
	constant := constantInstruction{}
	switch n.Type {
	case "i64":
		constant.kind, constant.number = integerConstant, parseInteger(n.Literal.(string), n.Token)
		constant.text = n.Literal.(string) // Preserve the artifact's original decimal spelling.
	case "String":
		constant.kind, constant.text = stringConstant, n.Literal.(string)
	case "bool":
		constant.kind, constant.boolean = booleanConstant, n.Literal.(bool)
	default:
		panic("Invalid literal in compiler AST")
	}
	f.emit(constant)
}

// Only this boundary constructs portable Instruction arrays. No checker or
// compiler control-flow pass relies on JSON operand positions.
func serializeFunction(fn *compilerFunction) *Function {
	result := &Function{Params: make([]string, len(fn.params)), ParamTypes: make([]string, len(fn.paramTypes)), Result: fn.result.String(), Receiver: fn.receiver.String(), Code: make([]Instruction, len(fn.code))}
	copy(result.Params, fn.params)
	for i, typ := range fn.paramTypes {
		result.ParamTypes[i] = typ.String()
	}
	for i, instruction := range fn.code {
		result.Code[i] = instruction.serialize()
	}
	return result
}

func (instruction constantInstruction) serialize() Instruction {
	switch instruction.kind {
	case integerConstant:
		text := instruction.text
		if text == "" {
			text = strconv.FormatInt(instruction.number, 10)
		}
		return Instruction{"PUSH", "i64", text}
	case stringConstant:
		return Instruction{"PUSH", "String", instruction.text}
	case booleanConstant:
		return Instruction{"PUSH", "bool", instruction.boolean}
	}
	panic("Invalid compiler constant")
}
func (instruction namedInstruction) serialize() Instruction {
	switch instruction.op {
	case irLoad:
		return Instruction{"LOAD", instruction.name}
	case irDeclare:
		return Instruction{"DECLARE", instruction.name}
	case irTrap:
		return Instruction{"TRAP", instruction.name}
	case irField:
		return Instruction{"FIELD", instruction.name}
	}
	panic("Invalid named compiler instruction")
}
func (instruction simpleInstruction) serialize() Instruction {
	switch irOpcode(instruction) {
	case irRef:
		return Instruction{"REF"}
	case irPrint:
		return Instruction{"PRINT"}
	case irPop:
		return Instruction{"POP"}
	case irEnter:
		return Instruction{"ENTER"}
	case irExit:
		return Instruction{"EXIT"}
	}
	panic("Invalid simple compiler instruction")
}
func (instruction pathInstruction) serialize() Instruction {
	switch instruction.op {
	case irLoadPath:
		return Instruction{"LOAD_PATH", instruction.names}
	case irStore:
		return Instruction{"STORE", instruction.names}
	}
	panic("Invalid compiler path instruction")
}
func (instruction structInstruction) serialize() Instruction {
	return Instruction{"STRUCT", instruction.typ.String(), instruction.names}
}
func (instruction enumInstruction) serialize() Instruction {
	return Instruction{"ENUM", instruction.typ.String(), instruction.name, instruction.count}
}
func (instruction *branchInstruction) serialize() Instruction {
	switch instruction.op {
	case irMatch:
		return Instruction{"MATCH_VARIANT", instruction.name, instruction.destination}
	case irAnd:
		return Instruction{"AND", instruction.destination}
	case irOr:
		return Instruction{"OR", instruction.destination}
	case irJump:
		return Instruction{"JUMP", instruction.destination}
	case irJumpFalse:
		return Instruction{"JUMP_FALSE", instruction.destination}
	}
	panic("Invalid compiler branch instruction")
}
func (instruction payloadInstruction) serialize() Instruction {
	return Instruction{"PAYLOAD", int(instruction)}
}
func (instruction tryInstruction) serialize() Instruction {
	return Instruction{"TRY", instruction.typ.String(), instruction.success}
}
func (instruction unaryInstruction) serialize() Instruction {
	operator := "-"
	if instruction {
		operator = "!"
	}
	return Instruction{"UNARY", operator}
}
func (instruction binaryInstruction) serialize() Instruction {
	return Instruction{"BINARY", arithmeticNames[instruction]}
}
func (instruction callInstruction) serialize() Instruction {
	switch instruction.op {
	case irCall:
		return Instruction{"CALL", instruction.name, instruction.count}
	case irCallMethod:
		return Instruction{"CALL_METHOD", instruction.name, instruction.count}
	}
	panic("Invalid compiler call instruction")
}
func (instruction returnInstruction) serialize() Instruction {
	return Instruction{"RETURN", bool(instruction)}
}
func (instruction collectionInstruction) serialize() Instruction {
	name := "List"
	if instruction.kind == mapCollection {
		name = "Map"
	}
	return Instruction{"COLLECTION", name, instruction.count}
}
func (instruction collectionCallInstruction) serialize() Instruction {
	contract := collectionContracts[instruction]
	return Instruction{"CALL_COLLECTION", contract.name, contract.arguments, contract.returns}
}
