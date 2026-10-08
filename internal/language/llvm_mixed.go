package language

import (
	"fmt"
	"slices"
	"strings"
)

// Unknown slots stay in the Go root frame. Only proven primitive slots may be
// read by native code; the exchange buffer never contains an object pointer.
const mixedUnknown valueKind = 255

type mixedAnalysis struct {
	states    []*scalarState
	native    []bool
	stackSize int
}

func primitive(k valueKind) bool { return k == intValue || k == boolValue }

func mixedStates(fn *preparedFunction, source *Function, metadata map[*preparedFunction]*Function) *mixedAnalysis {
	if source == nil || len(fn.code) == 0 {
		return nil
	}
	m := &mixedAnalysis{states: make([]*scalarState, len(fn.code)), native: make([]bool, len(fn.code)), stackSize: 1}
	initial := &scalarState{locals: make([]valueKind, fn.locals)}
	offset := 0
	if fn.receiver {
		initial.locals[0] = mixedUnknown
		offset = 1
	}
	if len(source.ParamTypes)+offset != fn.params {
		return nil
	}
	for n, typ := range source.ParamTypes {
		k := scalarKind(typ)
		if k == invalidValue {
			k = mixedUnknown
		}
		initial.locals[n+offset] = k
	}
	m.states[0] = initial
	type edge struct {
		target int
		state  scalarState
	}
	edges := make([][]edge, len(fn.code))
	queue := []int{0}
	for len(queue) > 0 {
		pc := queue[0]
		queue = queue[1:]
		edges[pc] = nil
		i := fn.code[pc]
		s := scalarState{stack: slices.Clone(m.states[pc].stack), locals: slices.Clone(m.states[pc].locals)}
		valid := true
		pop := func() valueKind {
			if len(s.stack) == 0 {
				valid = false
				return mixedUnknown
			}
			k := s.stack[len(s.stack)-1]
			s.stack = s.stack[:len(s.stack)-1]
			return k
		}
		push := func(k valueKind) {
			if k == invalidValue {
				k = mixedUnknown
			}
			s.stack = append(s.stack, k)
		}
		merge := func(target int, state scalarState) {
			if target < 0 || target >= len(m.states) {
				valid = false
				return
			}
			edges[pc] = append(edges[pc], edge{target, scalarState{stack: slices.Clone(state.stack), locals: slices.Clone(state.locals)}})
			old := m.states[target]
			if old == nil {
				m.states[target] = &scalarState{stack: slices.Clone(state.stack), locals: slices.Clone(state.locals)}
				queue = append(queue, target)
				return
			}
			if len(old.stack) != len(state.stack) {
				valid = false
				return
			}
			changed := false
			for n, k := range state.stack {
				if old.stack[n] != k && old.stack[n] != mixedUnknown {
					old.stack[n] = mixedUnknown
					changed = true
				}
			}
			for n, k := range state.locals {
				if old.locals[n] != k && old.locals[n] != mixedUnknown {
					old.locals[n] = mixedUnknown
					changed = true
				}
			}
			if changed {
				queue = append(queue, target)
			}
		}
		next, terminal := pc+1, false
		switch i.op {
		case codePush:
			push(i.constant.kind)
		case codeLoad:
			push(s.locals[i.slot])
		case codeReadPath, codeLoadPath:
			push(mixedUnknown)
		case codeDeclare, codeStoreLocal:
			s.locals[i.slot] = pop()
		case codeStoreField:
			pop()
		case codeCollection, codeEnum:
			for n := 0; n < i.count; n++ {
				pop()
			}
			push(mixedUnknown)
		case codeStruct:
			for range i.indices {
				pop()
			}
			push(mixedUnknown)
		case codeRef, codeField:
			pop()
			push(mixedUnknown)
		case codePayload:
			push(mixedUnknown)
		case codeTry:
			pop()
			push(mixedUnknown)
		case codeMatch:
			merge(i.jump, s)
		case codeCallCollection:
			for n := 0; n < i.count+1; n++ {
				pop()
			}
			if i.hasResult {
				k := mixedUnknown
				if i.collectionOp == collectionLen {
					k = intValue
				}
				if i.collectionOp == collectionContains {
					k = boolValue
				}
				push(k)
			}
		case codeCall:
			for n := 0; n < i.count; n++ {
				pop()
			}
			if i.target.result {
				k := mixedUnknown
				if t := metadata[i.target]; t != nil && primitive(scalarKind(t.Result)) {
					k = scalarKind(t.Result)
				}
				push(k)
			}
		case codeCallMethod:
			// The same method name can have different signatures on different
			// receivers. Leave those functions on the fully general helper path.
			return nil
		case codeBinary:
			b, a := pop(), pop()
			k := mixedUnknown
			if a == b && (a == intValue || a == boolValue && (i.arithmetic == opEqual || i.arithmetic == opNotEqual)) {
				k = intValue
				if i.arithmetic >= opEqual {
					k = boolValue
				}
			}
			push(k)
		case codeUnary:
			k := pop()
			if i.text == "!" && k == boolValue || i.text != "!" && k == intValue {
				push(k)
			} else {
				push(mixedUnknown)
			}
		case codePrint, codePop:
			pop()
		case codeEnter:
		case codeExit:
			for _, slot := range i.indices {
				s.locals[slot] = invalidValue
			}
		case codeJump:
			next = i.jump
		case codeJumpFalse:
			pop()
			merge(i.jump, s)
		case codeAnd, codeOr:
			merge(i.jump, s)
			pop()
		case codeReturn:
			if i.count != 0 {
				pop()
			}
			terminal = true
		case codeTrap:
			terminal = true
		default:
			return nil
		}
		if !valid {
			return nil
		}
		m.stackSize = max(m.stackSize, len(s.stack)+1)
		if !terminal {
			merge(next, s)
			if !valid {
				return nil
			}
		}
	}
	count := 0
	for pc, s := range m.states {
		if s == nil {
			continue
		}
		i := fn.code[pc]
		h := len(s.stack)
		top := mixedUnknown
		if h > 0 {
			top = s.stack[h-1]
		}
		native := false
		switch i.op {
		case codePush:
			native = primitive(i.constant.kind)
		case codeLoad:
			native = primitive(s.locals[i.slot])
		case codeDeclare, codeStoreLocal:
			native = primitive(top) && (s.locals[i.slot] == invalidValue || primitive(s.locals[i.slot]))
		case codeBinary:
			native = h >= 2 && s.stack[h-2] == top && (top == intValue || top == boolValue && (i.arithmetic == opEqual || i.arithmetic == opNotEqual))
		case codeUnary:
			native = i.text == "!" && top == boolValue || i.text != "!" && top == intValue
		case codePop:
			native = primitive(top)
		case codeEnter, codeJump:
			native = true
		case codeExit:
			native = true
			for _, slot := range i.indices {
				if s.locals[slot] != invalidValue && !primitive(s.locals[slot]) {
					native = false
				}
			}
		case codeJumpFalse, codeAnd, codeOr:
			native = top == boolValue
		}
		// A merge may erase type information. Materialize the edge in Go so a
		// formerly numeric slot remains valid when the successor treats it as a
		// general value. This also preserves objects on alternative branches.
		for _, edge := range edges[pc] {
			target := m.states[edge.target]
			for n, k := range edge.state.locals {
				if primitive(k) && target.locals[n] != k {
					native = false
				}
			}
			for n, k := range edge.state.stack {
				if primitive(k) && target.stack[n] != k {
					native = false
				}
			}
		}
		m.native[pc] = native
		if native {
			count++
		}
	}
	// Select loops that can stay native for repeated iterations. Splitting short
	// numeric runs around object helpers can cost more crossings than batching
	// the whole loop in Go; keep that path until region selection improves.
	nativeLoop := false
	for pc, i := range fn.code {
		if i.op != codeJump || i.jump >= pc || !m.native[pc] {
			continue
		}
		eligible := true
		for n := i.jump; n <= pc; n++ {
			if m.states[n] != nil && !m.native[n] {
				eligible = false
				break
			}
		}
		if eligible {
			nativeLoop = true
			break
		}
	}
	if count < 4 || !nativeLoop {
		return nil
	}
	return m
}

func emitMixedLLVM(out *strings.Builder, fn *preparedFunction, symbol string, m *mixedAnalysis) {
	e := &scalarEmitter{out: out}
	targets := map[int]bool{}
	for _, i := range fn.code {
		switch i.op {
		case codeJump, codeJumpFalse, codeAnd, codeOr, codeMatch:
			targets[i.jump] = true
		}
	}
	e.write("\ndefine i64 @%s(i64 %%frame, ptr %%callback, ptr %%budget) {\nentry:\n  %%locals = getelementptr i64, ptr %%budget, i64 4\n  %%stack = getelementptr i64, ptr %%budget, i64 %d\n  %%limitptr = getelementptr i64, ptr %%budget, i64 1\n  %%limit = load i64, ptr %%limitptr\n  br label %%pc0\n", symbol, 4+fn.locals)
	for pc := 0; pc < len(fn.code); pc++ {
		i := fn.code[pc]
		e.pc = pc
		e.write("pc%d:\n", pc)
		if m.states[pc] == nil {
			e.write("  br label %%failed\n")
			continue
		}
		next := fmt.Sprintf("pc%d", pc+1)
		if !m.native[pc] {
			end := pc
			for end+1 < len(fn.code) && end-pc+1 < 16 && !targets[end+1] && !m.native[end+1] && m.states[end+1] != nil && !endsNativeBatch(fn.code[end].op) && fn.code[end].op != codeTrap {
				end++
			}
			i = fn.code[end]
			v := e.temp()
			e.write("  %s = call i64 %%callback(i64 %%frame, i64 %d, i64 %d, i64 %d)\n  switch i64 %s, label %%failed [\n", v, pc, jitMixedBatch, end-pc+1, v)
			dest := map[int]bool{end + 1: true, len(fn.code) + 1: true}
			switch i.op {
			case codeJump:
				dest = map[int]bool{i.jump: true}
			case codeReturn, codeTrap:
				dest = map[int]bool{len(fn.code) + 1: true}
			case codeMatch, codeJumpFalse, codeAnd, codeOr:
				dest[i.jump] = true
			}
			for target := 0; target <= len(fn.code)+1; target++ {
				if dest[target] {
					label := fmt.Sprintf("pc%d", target)
					if target == len(fn.code)+1 {
						label = "returned"
					}
					e.write("    i64 %d, label %%%s\n", target, label)
				}
			}
			e.write("  ]\n")
			pc = end
			continue
		}
		e.write("  %%steps%d = load i64, ptr %%budget\n  %%charged%d = add i64 %%steps%d, 1\n  store i64 %%charged%d, ptr %%budget\n  %%allowed%d = icmp sle i64 %%charged%d, %%limit\n  br i1 %%allowed%d, label %%execute%d, label %%exceeded\nexecute%d:\n", pc, pc, pc, pc, pc, pc, pc, pc, pc)
		h := len(m.states[pc].stack)
		branch := true
		switch i.op {
		case codePush:
			e.store("stack", h, fmt.Sprint(i.constant.number))
		case codeLoad:
			e.store("stack", h, e.load("locals", i.slot))
		case codeDeclare, codeStoreLocal:
			e.store("locals", i.slot, e.load("stack", h-1))
		case codeBinary:
			e.store("stack", h-2, e.binary(i.arithmetic, e.load("stack", h-2), e.load("stack", h-1)))
		case codeUnary:
			a, r := e.load("stack", h-1), e.temp()
			if i.text == "!" {
				e.write("  %s = xor i64 %s, 1\n", r, a)
			} else {
				check := e.temp()
				e.write("  %s = icmp eq i64 %s, -9223372036854775808\n  br i1 %s, label %%overflow, label %%neg%d\nneg%d:\n  %s = sub i64 0, %s\n", check, a, check, pc, pc, r, a)
			}
			e.store("stack", h-1, r)
		case codeJump:
			next = fmt.Sprintf("pc%d", i.jump)
		case codeJumpFalse, codeAnd, codeOr:
			v, check := e.load("stack", h-1), e.temp()
			e.write("  %s = icmp ne i64 %s, 0\n", check, v)
			if i.op == codeOr {
				e.write("  br i1 %s, label %%pc%d, label %%%s\n", check, i.jump, next)
			} else {
				e.write("  br i1 %s, label %%%s, label %%pc%d\n", check, next, i.jump)
			}
			branch = false
		}
		if branch {
			e.write("  br label %%%s\n", next)
		}
	}
	e.write("pc%d:\n  br label %%failed\n", len(fn.code))
	e.pc = 0
	for _, trap := range []struct {
		label string
		code  int
	}{{"overflow", 1}, {"zero", 2}, {"exceeded", 4}} {
		e.write("%s:\n", trap.label)
		e.callback(jitFail, fmt.Sprint(trap.code))
		e.write("  br label %%failed\n")
	}
	e.write("returned:\n  ret i64 0\nfailed:\n  ret i64 -1\n}\n")
}

// Materialize only primitive slots. Unknown slots retain their Go references.
func (f *jitFrame) mixedImport(pc int) {
	s := f.mixed.states[pc]
	vm := f.vm
	for n, k := range s.locals {
		if primitive(k) {
			vm.locals[f.base+n] = value{kind: k, number: f.exchange[4+n]}
		}
	}
	want := vm.frameBase + len(s.stack)
	if want < len(vm.stack) {
		clear(vm.stack[want:])
		vm.stack = vm.stack[:want]
	}
	for len(vm.stack) < want {
		vm.stack = append(vm.stack, value{})
	}
	for n, k := range s.stack {
		if primitive(k) {
			vm.stack[vm.frameBase+n] = value{kind: k, number: f.exchange[4+f.fn.locals+n]}
		}
	}
}

func (f *jitFrame) mixedExport(pc int) {
	s := f.mixed.states[pc]
	if len(f.vm.stack)-f.vm.frameBase != len(s.stack) {
		fail("Invalid mixed stack shape")
	}
	for n, k := range s.locals {
		if primitive(k) {
			v := f.vm.locals[f.base+n]
			if v.kind != k {
				fail("Invalid mixed local type")
			}
			f.exchange[4+n] = v.number
		}
	}
	for n, k := range s.stack {
		if primitive(k) {
			v := f.vm.stack[f.vm.frameBase+n]
			if v.kind != k {
				fail("Invalid mixed stack type")
			}
			f.exchange[4+f.fn.locals+n] = v.number
		}
	}
}
