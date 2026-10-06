package language

// Semantic arithmetic operations are shared by checking, serialization,
// preparation and execution; they do not depend on either instruction format.
type arithmeticOp uint8

const (
	opAdd arithmeticOp = iota
	opSubtract
	opMultiply
	opDivide
	opRemainder
	opEqual
	opNotEqual
	opLess
	opGreater
	opLessEqual
	opGreaterEqual
)

var arithmeticNames = [...]string{"+", "-", "*", "/", "%", "==", "!=", "<", ">", "<=", ">="}
var arithmeticOps = map[string]arithmeticOp{"+": opAdd, "-": opSubtract, "*": opMultiply, "/": opDivide, "%": opRemainder, "==": opEqual, "!=": opNotEqual, "<": opLess, ">": opGreater, "<=": opLessEqual, ">=": opGreaterEqual}
