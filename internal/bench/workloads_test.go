package bench

import (
	"press/internal/language"
	"testing"
)

func TestWorkloadResults(t *testing.T) {
	for _, workload := range Workloads() {
		t.Run(workload.Name, func(t *testing.T) {
			if result := workload.Native(); result != workload.Expected {
				t.Fatalf("Native: %s", result)
			}
			program, err := language.Compile(workload.Source)
			if err != nil {
				t.Fatal(err)
			}
			output := ""
			count := 0
			err = language.Execute(program, language.Options{Print: func(s string) { output = s; count++ }})
			if err != nil {
				t.Fatal(err)
			}
			if count != 1 || output != workload.Expected {
				t.Fatalf("VM: %s (%d outputs)", output, count)
			}
		})
	}
}
