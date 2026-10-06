package language

import (
	"math"
	"strconv"
)

type valueKind uint8

const (
	invalidValue valueKind = iota
	intValue
	boolValue
	stringValue
	structValue
	referenceValue
	enumValue
)

// Primitive values live directly in stack/local slots, without interface boxing.
type value struct {
	kind   valueKind
	number int64
	text   string
	object *objectValue
}
type objectValue struct {
	entries map[value]value
	layout  *structLayout
	variant string
	fields  []value
}

func copyValue(v value) value {
	if v.kind == structValue || v.kind == enumValue {
		fields := make([]value, len(v.object.fields))
		for i, field := range v.object.fields {
			fields[i] = copyValue(field)
		}
		var entries map[value]value
		if v.object.entries != nil {
			entries = make(map[value]value, len(v.object.entries))
			for key, item := range v.object.entries {
				entries[key] = copyValue(item)
			}
		}
		v.object = &objectValue{layout: v.object.layout, variant: v.object.variant, fields: fields, entries: entries}
	}
	return v // Explicit references retain shared identity.
}
func object(v value) *objectValue {
	if (v.kind != structValue && v.kind != referenceValue) || v.object == nil {
		fail("Expected struct object")
	}
	return v.object
}
func formatValue(v value) string {
	switch v.kind {
	case intValue:
		return strconv.FormatInt(v.number, 10)
	case boolValue:
		return strconv.FormatBool(v.number != 0)
	case stringValue:
		return v.text
	}
	fail("Cannot print non-primitive value")
	return ""
}
func boolean(v bool) value {
	if v {
		return value{kind: boolValue, number: 1}
	}
	return value{kind: boolValue}
}
func arithmetic(op arithmeticOp, a, b value) value {
	if a.kind != b.kind {
		fail("Mismatched arithmetic operands")
	}
	if op == opEqual || op == opNotEqual {
		equal := false
		switch a.kind {
		case intValue, boolValue:
			equal = a.number == b.number
		case stringValue:
			equal = a.text == b.text
		default:
			fail("Equality requires primitive values")
		}
		if op == opNotEqual {
			equal = !equal
		}
		return boolean(equal)
	}
	if op == opAdd && a.kind == stringValue {
		return value{kind: stringValue, text: a.text + b.text}
	}
	if a.kind != intValue {
		fail("Arithmetic requires i64 operands")
	}
	left, right := a.number, b.number
	var result int64
	switch op {
	case opAdd:
		result = left + right
		if (right > 0 && result < left) || (right < 0 && result > left) {
			fail("i64 overflow")
		}
	case opSubtract:
		result = left - right
		if (right < 0 && result < left) || (right > 0 && result > left) {
			fail("i64 overflow")
		}
	case opMultiply:
		if (left == math.MinInt64 && right == -1) || (right == math.MinInt64 && left == -1) {
			fail("i64 overflow")
		}
		result = left * right
		if right != 0 && result/right != left {
			fail("i64 overflow")
		}
	case opDivide:
		if right == 0 {
			fail("Division by zero")
		}
		if left == math.MinInt64 && right == -1 {
			fail("i64 overflow")
		}
		result = left / right
	case opRemainder:
		if right == 0 {
			fail("Division by zero")
		}
		result = left % right
	case opLess:
		return boolean(left < right)
	case opGreater:
		return boolean(left > right)
	case opLessEqual:
		return boolean(left <= right)
	case opGreaterEqual:
		return boolean(left >= right)
	default:
		fail("Unknown arithmetic operator")
	}
	return value{kind: intValue, number: result}
}
