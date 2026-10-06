package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"press/internal/bench"
	"press/internal/language"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"
)

type measurement struct {
	Workload         string  `json:"workload"`
	Phase            string  `json:"phase"`
	Sample           int     `json:"sample"`
	Iterations       int     `json:"iterations"`
	NanosecondsPerOp float64 `json:"nsPerOp"`
	BytesPerOp       uint64  `json:"bytesPerOp"`
	AllocsPerOp      uint64  `json:"allocsPerOp"`
	GCCycles         uint32  `json:"gcCycles"`
	HeapAllocAfter   uint64  `json:"heapAllocAfter"`
	PauseNanoseconds uint64  `json:"gcPauseNanoseconds"`
	MemoryScope      string  `json:"memoryScope"`
}
type report struct {
	GoVersion    string        `json:"goVersion"`
	GOOS         string        `json:"goos"`
	GOARCH       string        `json:"goarch"`
	GOMAXPROCS   int           `json:"gomaxprocs"`
	Measurements []measurement `json:"measurements"`
}

func measure(name, phase string, sample, iterations int, minimum time.Duration, operation func() error) (measurement, error) {
	for {
		runtime.GC() // Standardize the initial heap; this collection is outside the timer.
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		for i := 0; i < iterations; i++ {
			if err := operation(); err != nil {
				return measurement{}, err
			}
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		if elapsed < minimum {
			if iterations >= 1<<28 {
				return measurement{}, fmt.Errorf("Could not reach minimum sample duration for %s/%s", name, phase)
			}
			factor := 10
			if elapsed > 0 {
				factor = int(float64(minimum)/float64(elapsed)*1.1) + 1
				if factor < 2 {
					factor = 2
				}
				if factor > 10 {
					factor = 10
				}
			}
			if iterations > (1<<28)/factor {
				iterations = 1 << 28
			} else {
				iterations *= factor
			}
			continue
		}
		return measurement{Workload: name, Phase: phase, Sample: sample, Iterations: iterations, NanosecondsPerOp: float64(elapsed.Nanoseconds()) / float64(iterations), BytesPerOp: (after.TotalAlloc - before.TotalAlloc) / uint64(iterations), AllocsPerOp: (after.Mallocs - before.Mallocs) / uint64(iterations), GCCycles: after.NumGC - before.NumGC, HeapAllocAfter: after.HeapAlloc, PauseNanoseconds: after.PauseTotalNs - before.PauseTotalNs, MemoryScope: "benchmark_process"}, nil
	}
}
func vmOperation(program *language.Program, expected string) func() error {
	return func() error {
		count := 0
		output := ""
		err := language.Execute(program, language.Options{Print: func(s string) { count++; output = s }})
		if err != nil {
			return err
		}
		if count != 1 || output != expected {
			return fmt.Errorf("Wrong VM result: %q, expected %q", output, expected)
		}
		return nil
	}
}
func run() error {
	iterations := flag.Int("iterations", 20, "minimum executions or compilations per sample (automatically calibrated)")
	minimum := flag.Duration("mintime", 100*time.Millisecond, "minimum measured duration per sample")
	samples := flag.Int("samples", 3, "independent samples per phase")
	cpuPath := flag.String("cpuprofile", "", "optional CPU profile path")
	heapPath := flag.String("heapprofile", "", "optional final live-heap profile path")
	jsonPath := flag.String("json", "", "optional machine-readable report path")
	cliPath := flag.String("cli", "", "optional native Press executable for launch/exec measurements")
	workloadName := flag.String("workload", "", "optional workload filter (e.g. nested_value_copies)")
	phaseName := flag.String("phase", "", "optional phase filter (compile, vm, load_execute, native_go, launch_exec)")
	flag.Parse()
	if flag.NArg() != 0 || *iterations <= 0 || *samples <= 0 || *minimum <= 0 {
		return fmt.Errorf("iterations, samples and mintime must be positive; positional arguments are not accepted")
	}
	if *phaseName != "" && *phaseName != "compile" && *phaseName != "vm" && *phaseName != "load_execute" && *phaseName != "native_go" && *phaseName != "launch_exec" {
		return fmt.Errorf("Unknown phase %s", *phaseName)
	}
	if *phaseName == "launch_exec" && *cliPath == "" {
		return fmt.Errorf("launch_exec requires -cli")
	}
	if *cpuPath != "" {
		file, err := os.Create(*cpuPath)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := pprof.StartCPUProfile(file); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	result := report{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GOMAXPROCS: runtime.GOMAXPROCS(0), Measurements: []measurement{}}
	fmt.Printf("%s %s/%s GOMAXPROCS=%d; one operation = one whole workload (loops: 1000 steps)\n", result.GoVersion, result.GOOS, result.GOARCH, result.GOMAXPROCS)
	fmt.Printf("%-22s %-12s %6s %12s %12s %10s %5s\n", "workload", "phase", "sample", "ns/op", "B/op", "allocs/op", "GC")
	selected := 0
	for _, workload := range bench.Workloads() {
		if *workloadName != "" && workload.Name != *workloadName {
			continue
		}
		selected++
		program, err := language.Compile(workload.Source)
		if err != nil {
			return err
		}
		operation := vmOperation(program, workload.Expected)
		if err := operation(); err != nil {
			return err
		}
		if workload.Native() != workload.Expected {
			return fmt.Errorf("Wrong native result for %s", workload.Name)
		}
		encoded, err := json.Marshal(program)
		if err != nil {
			return err
		}
		decoded, err := language.Decode(bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		if err := vmOperation(decoded, workload.Expected)(); err != nil {
			return err
		}
		phases := []struct {
			name      string
			operation func() error
		}{
			{"compile", func() error { _, err := language.Compile(workload.Source); return err }},
			{"vm", operation},
			{"load_execute", func() error {
				fresh, err := language.Decode(bytes.NewReader(encoded))
				if err != nil {
					return err
				}
				return vmOperation(fresh, workload.Expected)()
			}},
			{"native_go", func() error {
				if output := workload.Native(); output != workload.Expected {
					return fmt.Errorf("Wrong native result")
				}
				return nil
			}},
		}
		if *cliPath != "" {
			directory, err := os.MkdirTemp("", "press-bench-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(directory)
			artifact := filepath.Join(directory, "workload.pbc")
			if err := os.WriteFile(artifact, encoded, 0600); err != nil {
				return err
			}
			phases = append(phases, struct {
				name      string
				operation func() error
			}{"launch_exec", func() error {
				output, err := exec.Command(*cliPath, "exec", artifact).CombinedOutput()
				if err != nil {
					return fmt.Errorf("CLI failed: %w: %s", err, output)
				}
				if strings.TrimSpace(string(output)) != workload.Expected {
					return fmt.Errorf("Wrong CLI result: %s", output)
				}
				return nil
			}})
		}
		for _, phase := range phases {
			if *phaseName != "" && *phaseName != phase.name {
				continue
			}
			for sample := 1; sample <= *samples; sample++ {
				row, err := measure(workload.Name, phase.name, sample, *iterations, *minimum, phase.operation)
				if err != nil {
					return err
				}
				if phase.name == "launch_exec" {
					row.MemoryScope = "parent_process_only"
					fmt.Printf("%-22s %-12s %6d %12.0f %12s %10s %5s\n", row.Workload, row.Phase, row.Sample, row.NanosecondsPerOp, "parent only", "parent", "-")
				} else {
					fmt.Printf("%-22s %-12s %6d %12.0f %12d %10d %5d\n", row.Workload, row.Phase, row.Sample, row.NanosecondsPerOp, row.BytesPerOp, row.AllocsPerOp, row.GCCycles)
				}
				result.Measurements = append(result.Measurements, row)
			}
		}
	}
	if selected == 0 {
		return fmt.Errorf("Unknown workload %s", *workloadName)
	}
	if *heapPath != "" {
		file, err := os.Create(*heapPath)
		if err != nil {
			return err
		}
		runtime.GC()
		err = pprof.WriteHeapProfile(file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if *jsonPath != "" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*jsonPath, append(data, '\n'), 0644); err != nil {
			return err
		}
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
