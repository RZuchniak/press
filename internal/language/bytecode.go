package language

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sync"
)

// Instruction arrays preserve the original versioned JSON bytecode contract.
type Instruction []any
type Function struct {
	Params     []string      `json:"params"`
	ParamTypes []string      `json:"paramTypes,omitempty"`
	Result     string        `json:"result"`
	Code       []Instruction `json:"code"`
	Receiver   string        `json:"receiver,omitempty"`
}
type Program struct {
	Format     string                                `json:"format"`
	Version    int                                   `json:"version"`
	Structs    map[string]map[string]Field           `json:"structs"`
	Enums      map[string]map[string][]string        `json:"enums,omitempty"`
	Functions  map[string]*Function                  `json:"functions"`
	Interfaces map[string]map[string]MethodSignature `json:"interfaces,omitempty"`
	Methods    map[string]map[string]string          `json:"methods,omitempty"`
	// Executable metadata is immutable after first Execute. Concurrent runs
	// share only a prepared plan; the portable JSON representation is unchanged.
	prepareOnce  sync.Once
	prepared     *executionPlan
	prepareError error
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
func operandInt(v any) int {
	switch v := v.(type) {
	case int:
		if v >= 0 && v <= math.MaxInt32 {
			return v
		}
	case float64:
		if v >= 0 && v <= math.MaxInt32 && v == math.Trunc(v) {
			return int(v)
		}
	}
	fail("Invalid instruction operand")
	return 0
}
func operandStrings(v any) []string {
	switch v := v.(type) {
	case []string:
		return v
	case []any:
		r := make([]string, len(v))
		for i, s := range v {
			var ok bool
			r[i], ok = s.(string)
			if !ok {
				fail("Invalid string-list operand")
			}
		}
		return r
	}
	fail("Invalid string-list operand")
	return nil
}
