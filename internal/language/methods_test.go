package language

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func compileAndOutput(t *testing.T, source string) (*Program, []string) {
	t.Helper()
	program, err := Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	output := []string{}
	if err := Execute(decoded, Options{Print: func(s string) { output = append(output, s) }}); err != nil {
		t.Fatal(err)
	}
	return program, output
}
func expectOutput(t *testing.T, source string, expected ...string) *Program {
	t.Helper()
	program, output := compileAndOutput(t, source)
	if !slices.Equal(output, expected) {
		t.Fatalf("Output %q; expected %q", output, expected)
	}
	return program
}

func TestInterfacesExample(t *testing.T) {
	source, err := os.ReadFile("../../examples/interfaces.press")
	if err != nil {
		t.Fatal(err)
	}
	program := expectOutput(t, string(source), "1", "7", "7", "true", "false", "7", "2", "10", "hello")
	if program.Version != 3 {
		t.Fatal("Expected bytecode version 3")
	}
	if program.Functions["sameIdentity<Counter>"] == nil || program.Functions["sameIdentity<Counter>"].Receiver != "" {
		t.Fatal("Missing function specialization")
	}
	if program.Methods["Counter"]["increment"] != "Counter::increment" {
		t.Fatal("Missing dispatch table")
	}
}

const counterDefinition = `struct Counter {
 let count: i64;
 fn get() -> i64 { return self.count; }
 mut fn increment() { self.count += 1; }
 mut fn twice() { self.increment(); self.increment(); }
}`

func TestMethodReceiverSemantics(t *testing.T) {
	expectOutput(t, counterDefinition+`struct Outer { let inner: Counter; }
 fn main() {
  let c = Counter { count: 0 }; c.twice(); println(c.get());
  let copy = c; copy.increment(); println(c.get()); println(copy.get());
  let outer = Outer { inner: Counter { count: 0 } }; outer.inner.increment(); println(outer.inner.get());
  const shared = &Counter { count: 0 }; const alias = shared; alias.increment(); println(shared.get());
  println(Counter { count: 8 }.get());
 }`, "2", "2", "3", "1", "1", "8")
}
func TestReceiverCapturedBeforeArguments(t *testing.T) {
	expectOutput(t, `struct Counter { let count: i64; mut fn add(n: i64) { self.count += n; } }
 struct Holder { let counter: &Counter; }
 fn swap(holder: &Holder) -> i64 { holder.counter = &Counter { count: 100 }; return 1; }
 fn main() {
  const first = &Counter { count: 0 }; const holder = &Holder { counter: first };
  holder.counter.add(swap(holder)); println(first.count); println(holder.counter.count);
 }`, "1", "100")
}
func TestInterfaceDispatchUsesConcreteReceiver(t *testing.T) {
	expectOutput(t, `interface Describe { fn describe() -> String; mut fn update(); }
 struct A implements Describe { let n: i64; fn describe() -> String { return "A"; } mut fn update() { self.n += 1; } }
 struct B implements Describe { let n: i64; fn describe() -> String { return "B"; } mut fn update() { self.n += 2; } }
 fn use(item: &Describe) { println(item.describe()); item.update(); }
 fn forward(item: &Describe) -> &Describe { return item; }
 struct Holder { let item: &Describe; }
 fn main() {
  const a = &A { n: 0 }; const b = &B { n: 0 };
  let item: &Describe = a; use(item); item = b; use(forward(item));
  const holder = Holder { item: a }; holder.item.update();
  const optional: Option<&Describe> = Some(b);
  match (optional) { Some(value) => use(value), None => println("none") };
  println(a.n); println(b.n);
 }`, "A", "B", "B", "2", "4")
}
func TestGenericFunctionsAndStructs(t *testing.T) {
	program := expectOutput(t, `struct Box<T> { let value: T;
  fn get() -> T { return self.value; }
  mut fn set(value: T) { self.value = value; }
 }
 fn identity<T>(value: T) -> T { return value; }
 fn unwrap<T>(value: Option<T>, fallback: T) -> T { return match (value) { Some(n) => n, None => fallback }; }
 fn pass<T>(value: T) -> T { return identity(value); }
 fn recurse<T>(value: T, depth: i64) -> T { if (depth > 0) { return recurse(value, depth - 1); } return value; }
 fn main() {
  let box = Box<i64> { value: 1 }; box.set(2); println(box.get());
  const stringBox: Box<String> = Box { value: "text" }; println(stringBox.get());
  const inferred = Box { value: Some(3) }; println(unwrap(inferred.get(), 0));
  println(identity<i64>(4)); println(identity("hello")); println(pass(5));
  println(unwrap<i64>(None, 6)); println(recurse(7, 2));
 }`, "2", "text", "3", "4", "hello", "5", "6", "7")
	if program.Functions["identity<i64>"] == nil || program.Functions["identity<String>"] == nil || program.Structs["Box<i64>"] == nil {
		t.Fatal("Missing concrete specialization")
	}
	if program.Structs["Box<T>"] != nil {
		t.Fatal("Symbolic template leaked into bytecode")
	}
}
func TestGenericBoundsAndInterfaceArguments(t *testing.T) {
	expectOutput(t, `interface Id { fn id() -> i64; }
 interface Name { fn name() -> String; }
 struct User implements Id, Name { const number: i64; fn id() -> i64 { return self.number; } fn name() -> String { return "user"; } }
 struct Tagged<T: Id> { const value: T; fn id() -> i64 { return self.value.id(); } }
 fn identify<T: Id>(value: T) -> i64 { return value.id(); }
 fn describe<T: Id + Name>(value: T) -> String { return value.name(); }
 fn main() {
  const user = &User { number: 8 }; println(identify(user)); println(describe(user));
  println(identify<&Id>(user)); const erased: &Id = user; println(identify(erased));
  const tagged = Tagged { value: User { number: 9 } }; println(tagged.id());
 }`, "8", "user", "8", "8", "9")
}
func TestNonMutMethodsCanMutateExplicitSharedFields(t *testing.T) {
	expectOutput(t, counterDefinition+`struct Wrapper { const shared: &Counter;
  fn update() { self.shared.increment(); }
 }
 fn main() { const shared = &Counter { count: 0 }; const wrapper = Wrapper { shared: shared }; wrapper.update(); println(shared.count); }`, "1")
}
func TestGenericReferenceReceiverAndNestedTypes(t *testing.T) {
	expectOutput(t, `interface Increment { mut fn increment(); }
 struct Counter implements Increment { let count: i64; mut fn increment() { self.count += 1; } }
 struct Box<T> { let value: T; fn get() -> T { return self.value; } }
 fn increment<T: Increment>(value: &T) { value.increment(); }
 fn get<T>(value: &Box<T>) -> T { return value.get(); }
 fn main() {
  const counter = &Counter { count: 0 }; increment(counter); println(counter.count);
  const box = &Box { value: Some(Box { value: 5 }) };
  println(match (get(box)) { Some(inner) => inner.get(), None => 0 });
 }`, "1", "5")
}
func TestMethodSelfLoadsRemainValueCopies(t *testing.T) {
	expectOutput(t, counterDefinition+`struct Wrapper { let c: Counter;
 fn snapshot() -> Counter { return self.c; }
 mut fn changeCopy() { let copied = self.c; copied.increment(); }
 }
 fn main() {
  let wrapper = Wrapper { c: Counter { count: 2 } }; wrapper.changeCopy();
  let snapshot = wrapper.snapshot(); snapshot.increment(); println(wrapper.c.count); println(snapshot.count);
 }`, "2", "3")
}
func TestMethodsOnReturnedFields(t *testing.T) {
	expectOutput(t, counterDefinition+`struct Holder { const value: Counter; const shared: &Counter; }
 fn holder(shared: &Counter) -> Holder { return Holder { value: Counter { count: 4 }, shared: shared }; }
 fn main() { const shared = &Counter { count: 0 }; println(holder(shared).value.get()); holder(shared).shared.increment(); println(shared.count); }
 `, "4", "1")
}
func TestSpecializationLimits(t *testing.T) {
	for _, source := range []string{
		`fn expand<T>(value: T) { expand(Some(value)); } fn main() { expand(1); }`,
		`struct Recursive<T> { const value: Recursive<Option<T>>; } fn main() {}`,
	} {
		_, err := Compile(source)
		if err == nil || !strings.Contains(err.Error(), "specialization limit") {
			t.Fatalf("Expected specialization limit, got %v", err)
		}
	}
}
func TestMethodsInterfacesGenericsRejectInvalidPrograms(t *testing.T) {
	cases := []struct{ Source, Error string }{
		{`struct Box<T> { const value: T; fn bad() -> i64 { return self.value.id(); } } fn main() {}`, "no constrained method"},
		{`interface A { fn f() -> i64; } interface B { fn f() -> String; } fn unused<T: A + B>(value: T) {} fn main() {}`, "Conflicting interface bounds"},
		{`fn main() { const v = Some<i64>(1); }`, "do not accept explicit type arguments"},
		{`fn main() { println<i64>(1); }`, "not generic"},
		{counterDefinition + `fn main() { const c = Counter { count: 0 }; c.increment(); }`, "const receiver"},
		{counterDefinition + `struct W { const c: Counter; } fn main() { let w = W { c: Counter { count: 0 } }; w.c.increment(); }`, "const receiver"},
		{`struct S { let n: i64; fn bad() { self.n += 1; } } fn main() {}`, "Cannot assign"},
		{counterDefinition + `struct S { let c: Counter; fn bad() { self.c.increment(); } } fn main() {}`, "const receiver"},
		{`struct S { mut fn bad() { self = S {}; } } fn main() {}`, "Cannot replace self"},
		{`struct S { fn bad() { const self = 1; } } fn main() {}`, "self is reserved"},
		{`fn bad(self: i64) {} fn main() {}`, "self is reserved"},
		{`fn main() { println(self); }`, "Unknown variable self"},
		{`interface I { fn id() -> i64; } struct S implements I {} fn main() {}`, "missing interface method"},
		{`interface I { fn id() -> i64; } struct S implements I { fn id() -> String { return "bad"; } } fn main() {}`, "signature mismatch"},
		{`interface I { mut fn change(); } struct S implements I { fn change() {} } fn main() {}`, "signature mismatch"},
		{`struct S implements Missing {} fn main() {}`, "Unknown interface"},
		{`struct S { fn id() -> i64 { return 1; } } interface I { fn id() -> i64; } fn f(s: &I) {} fn main() { f(&S {}); }`, "Expected &I"},
		{`interface I {} fn f(s: I) {} fn main() {}`, "Invalid type I"},
		{`struct S { fn f() {} fn f() {} } fn main() {}`, "Duplicate method"},
		{`struct S { let f: i64; fn f() {} } fn main() {}`, "share name"},
		{`interface I { fn f(); fn f(); } fn main() {}`, "Duplicate interface method"},
		{`struct S { fn f<T>() {} } fn main() {}`, "Generic methods"},
		{`fn unused<T>(value: T) -> i64 { return value.id(); } fn main() {}`, "no constrained method"},
		{`fn unused<T>(value: T) -> T { return value + 1; } fn main() {}`, "Expected T"},
		{`fn unused<T: Missing>(value: T) {} fn main() {}`, "Unknown interface bound"},
		{`interface I { fn id() -> i64; } fn f<T: I>(value: T) -> i64 { return value.id(); } fn main() { println(f(1)); }`, "does not implement I"},
		{`struct Pair<T> { const a: T; const b: T; } fn main() { const pair = Pair { a: 1, b: true }; }`, "Conflicting inferred types"},
		{`struct Box<T> { const v: T; } fn use(value: Box<i64>) {} fn main() { use(Box<bool> { v: true }); }`, "Expected Box<i64>"},
		{`fn f<T>(a: T,b: T) {} fn main() { f(1,true); }`, "Conflicting inferred types"},
		{`fn f<T>(a: T) {} fn main() { f<i64,String>(1); }`, "Wrong type argument count"},
		{`fn f() {} fn main() { f<i64>(); }`, "not generic"},
		{`struct S {} fn main() { const s = S<i64> {}; }`, "Wrong type argument count"},
		{`fn f<T,T>(value: T) {} fn main() {}`, "Duplicate type parameter"},
		{`fn f<T>() -> T { return f<T>(); } fn main() { f(); }`, "Cannot infer T"},
		{`interface I { fn id() -> i64; } struct Box<T: I> { const v: T; } fn main() { const b = Box { v: 1 }; }`, "does not implement I"},
	}
	for _, tc := range cases {
		t.Run(tc.Error, func(t *testing.T) {
			_, err := Compile(tc.Source)
			if err == nil || !strings.Contains(err.Error(), tc.Error) {
				t.Fatalf("Expected %q, got %v\n%s", tc.Error, err, tc.Source)
			}
		})
	}
}
