// Package browser provides an editor-independent JSON boundary for the Go backend.
package browser

import (
	"encoding/json"
	"fmt"
	"press/internal/language"
)

const MaxRequestBytes = 1 << 20
const MaxOutputBytes = 64 << 10
const MaxSteps = 1_000_000
const MaxDepth = 256

type Request struct {
	Action   string `json:"action"`
	Source   string `json:"source"`
	MaxSteps int    `json:"maxSteps,omitempty"`
	MaxDepth int    `json:"maxDepth,omitempty"`
}
type Response struct {
	OK       bool     `json:"ok"`
	Stage    string   `json:"stage"`
	Error    string   `json:"error,omitempty"`
	Output   []string `json:"output"`
	Bytecode string   `json:"bytecode,omitempty"`
}

// HandleJSON only compiles source supplied by the caller; it exposes no filesystem,
// host callbacks or arbitrary bytecode loading. Deadlines belong to the worker host.
func HandleJSON(input string) (encoded string) {
	response := Response{Stage: "request", Output: []string{}}
	defer func() {
		if recovered := recover(); recovered != nil {
			response.OK, response.Stage = false, "internal"
			response.Error = fmt.Sprintf("Backend error: %v", recovered)
		}
		data, _ := json.Marshal(response)
		encoded = string(data)
	}()
	if len(input) > MaxRequestBytes {
		response.Error = "Request exceeds 1 MiB"
		return
	}
	var request Request
	if err := json.Unmarshal([]byte(input), &request); err != nil {
		response.Error = "Invalid request JSON: " + err.Error()
		return
	}
	if request.Action != "check" && request.Action != "compile" && request.Action != "run" {
		response.Error = "Action must be check, compile or run"
		return
	}
	if request.MaxSteps < 0 || request.MaxSteps > MaxSteps || request.MaxDepth < 0 || request.MaxDepth > MaxDepth {
		response.Error = "Execution limits outside supported range"
		return
	}
	response.Stage = "compile"
	program, err := language.Compile(request.Source)
	if err != nil {
		response.Error = err.Error()
		return
	}
	if request.Action == "compile" {
		data, err := json.Marshal(program)
		if err != nil {
			response.Error = err.Error()
			return
		}
		response.Bytecode = string(data)
	}
	if request.Action == "run" {
		response.Stage = "execute"
		outputBytes, exceeded := 0, false
		err := language.Execute(program, language.Options{MaxSteps: request.MaxSteps, MaxDepth: request.MaxDepth, Print: func(line string) {
			if outputBytes+len(line)+1 > MaxOutputBytes {
				exceeded = true
				panic("Output limit exceeded")
			}
			outputBytes += len(line) + 1
			response.Output = append(response.Output, line)
		}})
		if exceeded {
			response.Error = "Output limit exceeded (64 KiB)"
			return
		}
		if err != nil {
			response.Error = err.Error()
			return
		}
	}
	response.OK = true
	return
}
