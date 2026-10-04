// Package bench defines deterministic workloads shared by the benchmark CLI and
// its correctness checks. Native versions are reference implementations of the
// same algorithms, not emulations of the VM's map-based object representation.
package bench

import "strconv"

type Workload struct {
	Name     string
	Source   string
	Expected string
	Native   func() string
}

const iterations = 1000

type counter struct{ count int64 }

func (c *counter) increment() { c.count++ }

type incrementer interface{ increment() }

// Keep actual interface dispatch at this boundary rather than benchmarking a
// compiler-devirtualized constant receiver.
//
//go:noinline
func increment(value incrementer) { value.increment() }

//go:noinline
func double(value int64) int64 { return value * 2 }

func Workloads() []Workload {
	return []Workload{
		{Name: "empty_program", Expected: "0", Source: `fn main() { println(0); }`, Native: func() string { return "0" }},
		{Name: "arithmetic", Expected: "499500", Source: `fn main() {
   let sum = 0; let i = 0;
   while (i < 1000) { sum += i; i += 1; }
   println(sum);
  }`, Native: func() string {
			var sum int64
			for i := int64(0); i < iterations; i++ {
				sum += i
			}
			return strconv.FormatInt(sum, 10)
		}},
		{Name: "function_calls", Expected: "999000", Source: `fn double(value: i64) -> i64 { return value * 2; }
  fn main() { let sum = 0; let i = 0; while (i < 1000) { sum += double(i); i += 1; } println(sum); }`, Native: func() string {
			var sum int64
			for i := int64(0); i < iterations; i++ {
				sum += double(i)
			}
			return strconv.FormatInt(sum, 10)
		}},
		{Name: "nested_value_copies", Expected: "499500", Source: `struct Inner { let count: i64; }
  struct Outer { let inner: Inner; const label: String; }
  fn read(value: Outer) -> i64 { return value.inner.count; }
  fn main() { let value = Outer { inner: Inner { count: 0 }, label: "counter" }; let sum = 0; let i = 0;
   while (i < 1000) { value.inner.count = i; const copied = value; sum += read(copied); i += 1; } println(sum);
  }`, Native: func() string {
			type outer struct {
				inner counter
				label string
			}
			value := outer{label: "counter"}
			var sum int64
			for i := int64(0); i < iterations; i++ {
				value.inner.count = i
				copied := value
				sum += readOuter(copied.inner.count, copied.label)
			}
			return strconv.FormatInt(sum, 10)
		}},
		{Name: "shared_references", Expected: "1000", Source: `struct Counter { let count: i64; mut fn increment() { self.count += 1; } }
  fn main() { const value = &Counter { count: 0 }; let i = 0; while (i < 1000) { const alias = value; alias.increment(); i += 1; } println(value.count); }`, Native: func() string {
			value := &counter{}
			for i := 0; i < iterations; i++ {
				alias := value
				alias.increment()
			}
			return strconv.FormatInt(value.count, 10)
		}},
		{Name: "interface_dispatch", Expected: "1000", Source: `interface Incrementer { mut fn increment(); }
  struct Counter implements Incrementer { let count: i64; mut fn increment() { self.count += 1; } }
  fn bump(value: &Incrementer) { value.increment(); }
  fn main() { const value = &Counter { count: 0 }; let i = 0; while (i < 1000) { bump(value); i += 1; } println(value.count); }`, Native: func() string {
			value := &counter{}
			for i := 0; i < iterations; i++ {
				increment(value)
			}
			return strconv.FormatInt(value.count, 10)
		}},
		{Name: "allocation", Expected: "499500", Source: `struct Item { const id: i64; }
  fn main() { let sum = 0; let i = 0; while (i < 1000) { const item = &Item { id: i }; sum += item.id; i += 1; } println(sum); }`, Native: func() string {
			var sum int64
			for i := int64(0); i < iterations; i++ {
				item := allocateItem(i)
				sum += item.count
			}
			return strconv.FormatInt(sum, 10)
		}},
	}
}

//go:noinline
func readOuter(count int64, label string) int64 { return count }

//go:noinline
func allocateItem(id int64) *counter { return &counter{count: id} }
