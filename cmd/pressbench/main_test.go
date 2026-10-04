package main

import (
	"errors"
	"press/internal/language"
	"testing"
	"time"
)

func TestMeasurementCalibration(t *testing.T) {
	calls := 0
	row, err := measure("fixture", "vm", 1, 1, 2*time.Millisecond, func() error { calls++; time.Sleep(time.Millisecond); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if row.Iterations < 1 || calls < row.Iterations || row.NanosecondsPerOp*float64(row.Iterations) < float64(2*time.Millisecond) || row.MemoryScope != "benchmark_process" {
		t.Fatalf("Invalid measurement: %+v (%d calls)", row, calls)
	}
}
func TestMeasurementRejectsWrongResult(t *testing.T) {
	expected := errors.New("wrong result")
	if _, err := measure("fixture", "vm", 1, 1, time.Millisecond, func() error { return expected }); !errors.Is(err, expected) {
		t.Fatal(err)
	}
	program, err := language.Compile(`fn main() { println(1); }`)
	if err != nil {
		t.Fatal(err)
	}
	if err := vmOperation(program, "2")(); err == nil {
		t.Fatal("Wrong checksum accepted")
	}
}
