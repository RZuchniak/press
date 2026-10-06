package language

type opcode uint8

const (
	codePush opcode = iota
	codeLoad
	codeReadPath
	codeLoadPath
	codeDeclare
	codeStoreLocal
	codeStoreField
	codeStruct
	codeRef
	codeEnum
	codeMatch
	codePayload
	codeTry
	codeTrap
	codeField
	codeUnary
	codeBinary
	codeAnd
	codeOr
	codePrint
	codeCall
	codeCallMethod
	codeReturn
	codePop
	codeEnter
	codeExit
	codeJump
	codeJumpFalse
	codeBinaryLocal
	codeBinaryPush
	codeCompareJump
	codeCollection
	codeCallCollection
)

type structLayout struct {
	collection collectionKind
	name       string
	fields     map[string]int
	methods    map[string]*preparedFunction
}
type preparedInstruction struct {
	collection              collectionKind
	collectionOp            collectionOp
	hasResult               bool
	op                      opcode
	span, slot, count, jump int
	constant                value
	path                    []string
	text                    string
	indices                 []int
	layout                  *structLayout
	target                  *preparedFunction
	arithmetic              arithmeticOp
	rightSlot               int
	rightConstant           bool
	fallback                *preparedInstruction
}
type preparedFunction struct {
	name             string
	params, locals   int
	receiver, result bool
	code             []preparedInstruction
}
type executionPlan struct {
	functions map[string]*preparedFunction
	layouts   map[string]*structLayout
}
