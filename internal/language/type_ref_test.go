package language

import (
	"strings"
	"testing"
)

func TestTypeGraphInterningAndSubstitution(t *testing.T) {
	graph := newTypeGraph()
	original := graph.resolve("Result<List<&Box<T>>,Option<&Error>>")
	if original != graph.resolve(original.String()) {
		t.Fatal("Repeated resolution lost type identity")
	}
	if original.args[0].args[0].target.args[0] != graph.resolve("T") {
		t.Fatal("Nested types do not share interned children")
	}
	substituted := graph.substitute(original, map[string]string{"T": "Map<String,i64>"})
	if substituted != graph.resolve("Result<List<&Box<Map<String,i64>>>,Option<&Error>>") {
		t.Fatal(substituted)
	}
	if original.String() != "Result<List<&Box<T>>,Option<&Error>>" {
		t.Fatal("Substitution mutated its input")
	}
	if substituted.args[1] != original.args[1] {
		t.Fatal("Substitution did not preserve unchanged children")
	}
	other := newTypeGraph()
	if other.resolve(original.String()) == original {
		t.Fatal("Type nodes escaped compilation ownership")
	}
}

func TestInvalidTypeShapes(t *testing.T) {
	for _, text := range []string{"List<>", "Map<String,>", "List<i64", "List<i64>>", "&", "i64!"} {
		t.Run(text, func(t *testing.T) {
			var err error
			func() { defer recoverLanguage(&err); newTypeGraph().resolve(text) }()
			if err == nil || !strings.Contains(err.Error(), "Invalid type") {
				t.Fatal(err)
			}
		})
	}
}

func TestNestedGenericShapesAfterTypeMigration(t *testing.T) {
	expectOutput(t, `
struct Box<T> { let value: T; }
fn lookup<T>(values: Map<String,List<&Box<T>>>) -> Option<List<&Box<T>>> { return values.get("key"); }
fn identity<T>(value: Result<Option<List<T>>,String>) -> Result<Option<List<T>>,String> { return value; }
fn main() {
 const box = &Box<i64> { value: 7 };
 let values = Map<String,List<&Box<i64>>>();
 values.set("key", List<&Box<i64>>(box));
 let list = match (lookup(values)) { Some(v) => v, None => List<&Box<i64>>() };
 const recovered = match (list.get(0)) { Some(v) => v, None => &Box<i64> { value: 0 } };
 recovered.value = 9; println(box.value);
 const result: Result<Option<List<i64>>,String> = Ok(Some(List<i64>(3)));
 println(match (identity(result)) {
  Ok(option) => match (option) { Some(values) => values.len(), None => 0 },
  Err(message) => -1
 });
}`, "9", "1")
	_, err := Compile(`fn same<T>(a: List<&T>, b: List<&T>) {} struct A {} struct B {} fn main() { same(List<&A>(&A {}),List<&B>(&B {})); }`)
	if err == nil || !strings.Contains(err.Error(), "Conflicting inferred types") {
		t.Fatal(err)
	}
}
