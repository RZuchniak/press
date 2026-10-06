package language

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListValuesAndBounds(t *testing.T) {
	expectOutput(t, `
fn take(value: Option<i64>) -> i64 { return match (value) { Some(n) => n, None => -1 }; }
fn main() {
 let original = List<i64>(10, 20);
 let copied = original;
 copied.push(30);
 println(original.len()); println(copied.len());
 println(copied.set(0, 99)); println(take(original.get(0)));
 println(take(copied.get(0))); println(take(copied.get(-1)));
 println(take(copied.get(9223372036854775807)));
 println(copied.set(3, 1)); println(take(copied.pop()));
 println(take(copied.pop())); println(take(copied.pop())); println(take(copied.pop()));
}`, "2", "3", "true", "10", "99", "-1", "-1", "false", "30", "20", "99", "-1")
}

func TestMapValuesAndReplacement(t *testing.T) {
	expectOutput(t, `
fn take(value: Option<i64>) -> i64 { return match (value) { Some(n) => n, None => -1 }; }
fn main() {
 let scores = Map<String,i64>();
 println(take(scores.set("Ada", 10)));
 println(take(scores.set("Ada", 20)));
 let copy = scores;
 copy.set("Ada", 30);
 println(take(scores.get("Ada"))); println(take(copy.get("Ada")));
 println(copy.len()); println(copy.contains("Ada"));
 println(take(copy.remove("Ada"))); println(copy.contains("Ada"));
 println(take(copy.remove("Ada"))); println(take(copy.get("Missing")));
 let flags = Map<bool,String>(); flags.set(true, "yes");
 println(match (flags.get(true)) { Some(s) => s, None => "no" });
 let numbers = Map<i64,bool>(); numbers.set(-2, true);
 println(match (numbers.get(-2)) { Some(b) => b, None => false });
}`, "-1", "10", "20", "30", "1", "true", "30", "false", "-1", "-1", "yes", "true")
}

func TestSharedCollectionsAndNestedCopies(t *testing.T) {
	expectOutput(t, `
struct Item { let count: i64; mut fn increment() { self.count += 1; } }
fn append(items: &List<i64>) { items.push(7); }
fn main() {
 const shared = &List<i64>(); const alias = shared;
 append(alias); println(shared.len());
 const map = &Map<String,i64>(); const other = map;
 other.set("key", 4); println(map.contains("key"));
 let items = List<Item>(Item { count: 1 });
 let item = match (items.get(0)) { Some(i) => i, None => Item { count: 0 } };
 item.increment(); println(item.count);
 println(match (items.get(0)) { Some(i) => i.count, None => -1 });
 let nested = List<List<i64>>(List<i64>(8));
 let inner = match (nested.get(0)) { Some(i) => i, None => List<i64>() };
 inner.push(9); println(inner.len());
 println(match (nested.get(0)) { Some(i) => i.len(), None => -1 });
 let references = List<&List<i64>>(shared);
 const extracted = match (references.get(0)) { Some(i) => i, None => &List<i64>() };
 extracted.push(8); println(shared.len());
}`, "1", "true", "2", "1", "2", "1", "2")
}

func TestCollectionsInGenericFunctions(t *testing.T) {
	expectOutput(t, `
fn first<T>(items: List<T>) -> Option<T> { return items.get(0); }
fn append<T>(items: &List<T>, item: T) { items.push(item); }
fn main() {
 const items = &List<String>(); append(items, "hello");
 println(match (items.get(0)) { Some(s) => s, None => "missing" });
 println(match (first(List<i64>(4))) { Some(n) => n, None => -1 });
}`, "hello", "4")
}

func TestRecursiveCollectionValues(t *testing.T) {
	expectOutput(t, `
struct Node { let children: List<Node>; }
fn main() {
 let tree = Node { children: List<Node>() };
 tree.children.push(Node { children: List<Node>() });
 let copy = tree;
 copy.children.push(Node { children: List<Node>() });
 println(tree.children.len()); println(copy.children.len());
}`, "1", "2")
}

func TestMapNestedValueCopies(t *testing.T) {
	expectOutput(t, `
fn first<T>(items: Map<String,T>) -> Option<T> { return items.get("key"); }
fn main() {
 let map = Map<String,List<i64>>(); map.set("key", List<i64>(1));
 let copied = map;
 let retrieved = match (first(copied)) { Some(v) => v, None => List<i64>() };
 retrieved.push(2); copied.set("key", retrieved);
 println(match (map.get("key")) { Some(v) => v.len(), None => -1 });
 println(match (copied.get("key")) { Some(v) => v.len(), None => -1 });
}`, "1", "2")
}

func TestCollectionReceiverCapturedBeforeArgumentEffects(t *testing.T) {
	expectOutput(t, `
struct Holder { let items: List<i64>; }
fn replace(holder: &Holder) -> i64 { holder.items = List<i64>(99); return 2; }
fn main() {
 const holder = &Holder { items: List<i64>(1) };
 holder.items.push(replace(holder));
 println(holder.items.len());
 println(match (holder.items.get(0)) { Some(n) => n, None => -1 });
}`, "1", "99")
}

func TestCollectionErrors(t *testing.T) {
	cases := []struct{ source, error string }{
		{`fn main() { let x = List(); }`, "Invalid type"},
		{`fn main() { let x = List<i64,String>(); }`, "Invalid type"},
		{`fn main() { let x = Map<i64>(); }`, "Invalid type"},
		{`fn main() { let x = Map<List<i64>,i64>(); }`, "Map keys"},
		{`fn main() { let x = Map<String,i64>(1); }`, "no arguments"},
		{`fn main() { let x = List<i64>("bad"); }`, "Expected i64"},
		{`fn main() { const x = List<i64>(); x.push(1); }`, "const receiver"},
		{`fn main() { const x = Map<String,i64>(); x.set("a",1); }`, "const receiver"},
		{`fn main() { let x = List<i64>(); x.get("bad"); }`, "Expected i64"},
		{`fn main() { let x = List<i64>(); x.get(); }`, "Wrong argument count"},
		{`fn main() { let x = Map<String,i64>(); x.push(1); }`, "push requires List"},
		{`fn main() { List<i64>().push(1); }`, "mutable value binding"},
		{`fn main() { let x = List<i64>(); x.unknown(); }`, "Unknown collection method"},
		{`struct List {} fn main() {}`, "reserved type"},
	}
	for _, test := range cases {
		t.Run(test.source, func(t *testing.T) {
			_, err := Compile(test.source)
			if err == nil || !strings.Contains(err.Error(), test.error) {
				t.Fatalf("got %v; want %s", err, test.error)
			}
		})
	}
}

func TestModulesLinkedIntoPortableArtifact(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.press":   `import "left.press"; import "right.press"; fn main() { println(left()); println(right()); }`,
		"left.press":   `import "common.press"; fn left() -> i64 { return size(List<i64>(1,2)); }`,
		"right.press":  `import "common.press"; fn right() -> i64 { return size(List<i64>(3)); }`,
		"common.press": `fn size<T>(items: List<T>) -> i64 { return items.len(); }`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	program, err := CompileFile(filepath.Join(dir, "main.press"))
	if err != nil {
		t.Fatal(err)
	}
	if program.Version != 4 {
		t.Fatalf("version %d", program.Version)
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	program, err = Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	var output []string
	if err := Execute(program, Options{Print: func(s string) { output = append(output, s) }}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(output, ",") != "2,1" {
		t.Fatal(output)
	}
}

func TestModuleErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.press", `import "b.press"; fn main() {}`)
	write("b.press", `import "a.press";`)
	if _, err := CompileFile(filepath.Join(dir, "a.press")); err == nil || !strings.Contains(err.Error(), "Import cycle") {
		t.Fatalf("%v", err)
	}
	write("b.press", `fn main() {}`)
	if _, err := CompileFile(filepath.Join(dir, "a.press")); err == nil || !strings.Contains(err.Error(), "Duplicate") {
		t.Fatalf("%v", err)
	}
	write("b.press", `fn bad() { @ }`)
	if _, err := CompileFile(filepath.Join(dir, "a.press")); err == nil || !strings.Contains(err.Error(), "b.press") {
		t.Fatalf("%v", err)
	}
	write("a.press", `import "missing.press"; fn main() {}`)
	if _, err := CompileFile(filepath.Join(dir, "a.press")); err == nil || !strings.Contains(err.Error(), "missing.press") {
		t.Fatalf("%v", err)
	}
	if _, err := Compile(`import "a.press"; fn main() {}`); err == nil {
		t.Fatal("Compile silently ignored imports")
	}
}
