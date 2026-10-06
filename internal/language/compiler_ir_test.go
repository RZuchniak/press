package language

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// These artifacts were captured before the typed type/IR migration. Comparing
// the full wire representation protects external consumers, branch addresses,
// method/generic specialization names, and literal encoding across the refactor.
func TestCompilerArtifactCompatibility(t *testing.T) {
	data, err := os.ReadFile("testdata/compiler-artifacts.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Source   string   `json:"source"`
		File     string   `json:"file"`
		Artifact *Program `json:"artifact"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 28 {
		t.Fatalf("Incomplete artifact baseline: %d", len(cases))
	}
	for i, fixture := range cases {
		var program *Program
		if fixture.File != "" {
			program, err = CompileFile(fixture.File)
		} else {
			program, err = Compile(fixture.Source)
		}
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		actual, err := json.Marshal(program)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := json.Marshal(fixture.Artifact)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("Artifact changed for case %d", i)
		}
	}
}

func TestIRControlFlowKeepsEvaluationOrder(t *testing.T) {
	expectOutput(t, `
struct State { let calls: i64; }
fn visit(state: &State) -> bool { state.calls += 1; return true; }
fn main() {
 const state = &State { calls: 0 };
 println(false && visit(state)); println(true || visit(state));
 println(true && visit(state)); println(false || visit(state));
 let i = 0;
 while (i < 3) { if (i == 1) { println(10); } else { println(i); } i += 1; }
 println(state.calls);
 println(0007); println(-9223372036854775808);
}`, "false", "true", "true", "true", "0", "10", "2", "2", "7", "-9223372036854775808")
}
