package language

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestExpressionSnapshotsAcrossSideEffects(t *testing.T) {
	expectOutput(t, `struct Item { let n: i64; }
 struct Container { let child: Item;
  mut fn change() -> i64 { self.child.n = 99; return 0; }
  mut fn check() -> i64 { return read(self.child, self.change()); }
 }
 fn read(item: Item, unused: i64) -> i64 { return item.n; }
 fn main() {
  let container = Container { child: Item { n: 1 } };
  println(container.check()); println(container.child.n);
  const shared = &Container { child: Item { n: 2 } };
  let snapshot = shared.child; snapshot.n = 3;
  println(shared.child.n); println(snapshot.n);
 }`, "1", "99", "2", "3")
}
func TestOwnedValuesAcrossCallsAndEnums(t *testing.T) {
	expectOutput(t, `struct Item { let n: i64; }
 fn returned(item: Item) -> Item { return item; }
 fn wrapped(item: Item) -> Option<Item> { return Some(item); }
 fn main() {
  let original = Item { n: 1 };
  let copied = returned(original); copied.n = 2;
  let holder = wrapped(original);
  let extracted = match (holder) { Some(item) => item, None => Item { n: 0 } };
  extracted.n = 3;
  println(original.n); println(copied.n); println(extracted.n);
  println(match (holder) { Some(item) => item.n, None => 0 });
 }`, "1", "2", "3", "1")
}
func cloneProgram(t *testing.T, p *Program) *Program {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return clone
}
func unfusedProgram(t *testing.T, p *Program) *Program {
	t.Helper()
	clone := cloneProgram(t, p)
	plan, err := prepare(clone)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range plan.functions {
		for i, instruction := range fn.code {
			if instruction.fallback != nil {
				fn.code[i] = *instruction.fallback
			}
		}
	}
	clone.prepareOnce.Do(func() { clone.prepared = plan })
	return clone
}
func TestFusedInstructionBudgetsAndErrorOrder(t *testing.T) {
	for _, source := range []string{
		`fn main() { let sum = 0; let i = 0; while (i < 4) { sum += i; i += 1; println(sum); } }`,
		`struct Inner { const n: i64; } struct Outer { const inner: Inner; } fn main() { const o = Outer { inner: Inner { n: 7 } }; println(o.inner.n); }`,
		`fn main() { let n = 9223372036854775807; n += 1; println(n); }`,
		`fn main() { let n = 1; let zero = 0; n = n / zero; println(n); }`,
	} {
		program, err := Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		reference := unfusedProgram(t, program)
		for budget := 1; budget <= 80; budget++ {
			got, want := []string{}, []string{}
			gotErr := Execute(program, Options{MaxSteps: budget, Print: func(s string) { got = append(got, s) }})
			wantErr := Execute(reference, Options{MaxSteps: budget, Print: func(s string) { want = append(want, s) }})
			if !slices.Equal(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("Budget %d: %q %v; wanted %q %v", budget, got, gotErr, want, wantErr)
			}
		}
	}
}
func TestPreparedProgramConcurrentExecution(t *testing.T) {
	source, err := Compile(`struct Counter { let n: i64; mut fn bump() { self.n += 1; } }
 fn main() { const c = &Counter { n: 0 }; let i = 0; while (i < 20) { c.bump(); i += 1; } println(c.n); }`)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errors := make(chan error, 32)
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			output := ""
			err := Execute(source, Options{Print: func(s string) { output = s }})
			if err != nil {
				errors <- err
			} else if output != "20" {
				errors <- fmt.Errorf("Guest state leaked: %q", output)
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
func TestScratchCleanupOnErrorAndLexicalExit(t *testing.T) {
	program, err := Compile(`struct S { const n: i64; } fn main() { if (true) { const value = &S { n: 1 }; println(value.n); } println(1 / 0); }`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		err := Execute(program, Options{Print: func(string) {}})
		if err == nil || !strings.Contains(err.Error(), "Division by zero") {
			t.Fatal(err)
		}
	}
	expectOutput(t, `fn main() { println(42); }`, "42")
	plan, err := prepare(program)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, instruction := range plan.functions["main"].code {
		if instruction.op == codeExit && len(instruction.indices) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("Lexical exit does not release local slots")
	}
}
