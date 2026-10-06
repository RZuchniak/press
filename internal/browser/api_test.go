package browser

import (
	"encoding/json"
	"strings"
	"testing"
)

func call(t *testing.T, request Request) Response {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal([]byte(HandleJSON(string(data))), &response); err != nil {
		t.Fatal(err)
	}
	return response
}
func TestBrowserAPI(t *testing.T) {
	source := `fn main() { println(9223372036854775807); }`
	for _, action := range []string{"check", "compile", "run"} {
		response := call(t, Request{Action: action, Source: source})
		if !response.OK {
			t.Fatal(response)
		}
		if action == "run" && strings.Join(response.Output, ",") != "9223372036854775807" {
			t.Fatal(response)
		}
		if action == "compile" && !strings.Contains(response.Bytecode, `"9223372036854775807"`) {
			t.Fatal("Lost integer precision", response)
		}
		if action != "run" && len(response.Output) != 0 {
			t.Fatal("Checking/compilation executed source")
		}
	}
}
func TestBrowserErrorsAndRecovery(t *testing.T) {
	for _, test := range []struct {
		request      Request
		stage, error string
	}{
		{Request{Action: "run", Source: `fn main() { const x = 1; x = 2; }`}, "compile", "Cannot assign"},
		{Request{Action: "run", Source: `fn main() { println(1 / 0); }`}, "execute", "Division by zero"},
		{Request{Action: "run", Source: `fn main() { while (true) {} }`, MaxSteps: 20}, "execute", "Instruction limit"},
		{Request{Action: "run", Source: `fn f() { f(); } fn main() { f(); }`, MaxDepth: 10}, "execute", "Call depth"},
		{Request{Action: "run", Source: `fn main() { while (true) { println("abcdefghijklmnopqrstuvwxyz"); } }`}, "execute", "Output limit"},
		{Request{Action: "run", MaxSteps: MaxSteps + 1}, "request", "Execution limits"},
		{Request{Action: "exec"}, "request", "Action must"},
	} {
		response := call(t, test.request)
		if response.OK || response.Stage != test.stage || !strings.Contains(response.Error, test.error) {
			t.Fatal(response)
		}
		if good := call(t, Request{Action: "run", Source: `fn main() { println(42); }`}); !good.OK {
			t.Fatal("Backend did not recover", good)
		}
	}
	for _, input := range []string{"{", strings.Repeat("x", MaxRequestBytes+1)} {
		var response Response
		if err := json.Unmarshal([]byte(HandleJSON(input)), &response); err != nil || response.OK {
			t.Fatal(err, response)
		}
	}
}
