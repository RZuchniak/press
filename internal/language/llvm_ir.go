package language

import (
	"fmt"
	"sort"
	"strings"
)

// Scalar instructions and batched helper blocks retain original bytecode PCs.
// Native frames carry integer root IDs; Go owns all object-bearing slots.
func emitLLVM(plan *executionPlan, program *Program) (string, map[*preparedFunction]string, map[*preparedFunction]*mixedAnalysis) {
	var out strings.Builder
	out.WriteString("; Press experimental LLVM backend: Go-owned roots\n")
	for _, op := range []string{"sadd", "ssub", "smul"} {
		fmt.Fprintf(&out, "declare { i64, i1 } @llvm.%s.with.overflow.i64(i64, i64)\n", op)
	}
	names := make([]string, 0, len(plan.functions))
	for name := range plan.functions {
		names = append(names, name)
	}
	sort.Strings(names)
	symbols := make(map[*preparedFunction]string, len(names))
	for index, name := range names {
		symbols[plan.functions[name]] = fmt.Sprintf("press_f%d", index)
	}
	metadata := make(map[*preparedFunction]*Function, len(names))
	for _, name := range names {
		metadata[plan.functions[name]] = program.Functions[name]
	}
	scalar := make(map[*preparedFunction][]*scalarState, len(names))
	for _, name := range names {
		fn := plan.functions[name]
		if states := scalarStates(fn, metadata[fn], metadata); states != nil {
			scalar[fn] = states
		}
	}
	// Prune callers of unsupported bodies to a fixed point. Recursive scalar
	// components remain eligible; no native call may target the helper ABI.
	for changed := true; changed; {
		changed = false
		for fn, states := range scalar {
			for pc, i := range fn.code {
				if states[pc] != nil && i.op == codeCall && scalar[i.target] == nil {
					delete(scalar, fn)
					changed = true
					break
				}
			}
		}
	}
	mixed := make(map[*preparedFunction]*mixedAnalysis)
	for _, name := range names {
		fn := plan.functions[name]
		symbol := symbols[fn]
		if states := scalar[fn]; states != nil {
			emitScalarLLVM(&out, fn, metadata[fn], symbol, states, symbols)
			continue
		}
		if analysis := mixedStates(fn, metadata[fn], metadata); analysis != nil {
			mixed[fn] = analysis
			emitMixedLLVM(&out, fn, symbol, analysis)
			continue
		}
		fmt.Fprintf(&out, "\ndefine i64 @%s(i64 %%frame, ptr %%callback, ptr %%budget) {\nentry:\n  br label %%pc0\n", symbol)
		targets := map[int]bool{}
		for _, i := range fn.code {
			switch i.op {
			case codeJump, codeJumpFalse, codeAnd, codeOr, codeMatch:
				targets[i.jump] = true
			}
		}
		for pc := 0; pc < len(fn.code); {
			end := pc
			for end+1 < len(fn.code) && end-pc+1 < 16 && !targets[end+1] && !endsNativeBatch(fn.code[end].op) {
				end++
			}
			i := fn.code[end]
			count := end - pc + 1
			fmt.Fprintf(&out, "pc%d:\n", pc)
			fmt.Fprintf(&out, "  %%next%d = call i64 %%callback(i64 %%frame, i64 %d, i64 %d, i64 %d)\n", pc, pc, jitBatch, count)
			// Explicit destinations avoid a runtime opcode dispatcher in native code.
			destinations := map[int]bool{end + 1: true, len(fn.code) + 1: true}
			switch i.op {
			case codeJump:
				destinations = map[int]bool{i.jump: true}
			case codeJumpFalse, codeAnd, codeOr, codeMatch:
				destinations[i.jump] = true
			case codeReturn:
				destinations = map[int]bool{len(fn.code) + 1: true}
			}
			keys := make([]int, 0, len(destinations))
			for target := range destinations {
				keys = append(keys, target)
			}
			sort.Ints(keys)
			fmt.Fprintf(&out, "  switch i64 %%next%d, label %%failed [\n", pc)
			for _, target := range keys {
				label := fmt.Sprintf("pc%d", target)
				if target == len(fn.code)+1 {
					label = "returned"
				}
				fmt.Fprintf(&out, "    i64 %d, label %%%s\n", target, label)
			}
			out.WriteString("  ]\n")
			pc = end + 1
		}
		fmt.Fprintf(&out, "pc%d:\n  %%fell = call i64 %%callback(i64 %%frame, i64 0, i64 %d, i64 3)\n  br label %%failed\n", len(fn.code), jitFail)
		out.WriteString("returned:\n  ret i64 0\nfailed:\n  ret i64 -1\n}\n")
	}
	return out.String(), symbols, mixed
}

func endsNativeBatch(op opcode) bool {
	switch op {
	case codeJump, codeJumpFalse, codeAnd, codeOr, codeMatch, codeReturn, codeTry, codeCall, codeCallMethod:
		return true
	}
	return false
}
