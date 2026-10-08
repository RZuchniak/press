package language

import (
	"fmt"
	"slices"
	"strings"
)

type scalarState struct{ stack, locals []valueKind }

func scalarKind(typ string) valueKind {
	switch typ {
	case "i64":
		return intValue
	case "bool":
		return boolValue
	}
	return invalidValue
}

// Prove stack shapes and initialized scalar local types across reachable control
// flow before placing values on a native stack. Any uncertainty uses Go helpers.
func scalarStates(fn *preparedFunction, source *Function, callees ...map[*preparedFunction]*Function) []*scalarState {
	if fn.receiver || len(source.ParamTypes) != fn.params || source.Result != "void" && scalarKind(source.Result) == invalidValue {
		return nil
	}
	states := make([]*scalarState, len(fn.code))
	if len(states) == 0 {
		return nil
	}
	initial := &scalarState{locals: make([]valueKind, fn.locals)}
	for slot, typ := range source.ParamTypes {
		initial.locals[slot] = scalarKind(typ)
		if initial.locals[slot] == invalidValue {
			return nil
		}
	}
	states[0] = initial
	queue := []int{0}
	for len(queue) > 0 {
		pc := queue[0]
		queue = queue[1:]
		i := fn.code[pc]
		s := scalarState{stack: slices.Clone(states[pc].stack), locals: slices.Clone(states[pc].locals)}
		valid := true
		pop := func() valueKind {
			if len(s.stack) == 0 {
				valid = false
				return invalidValue
			}
			k := s.stack[len(s.stack)-1]
			s.stack = s.stack[:len(s.stack)-1]
			return k
		}
		push := func(k valueKind) {
			if k == invalidValue {
				valid = false
			}
			s.stack = append(s.stack, k)
		}
		merge := func(target int, state scalarState) {
			if target < 0 || target >= len(states) {
				valid = false
				return
			}
			if old := states[target]; old != nil {
				if !slices.Equal(old.stack, state.stack) || !slices.Equal(old.locals, state.locals) {
					valid = false
				}
			} else {
				states[target] = &scalarState{stack: slices.Clone(state.stack), locals: slices.Clone(state.locals)}
				queue = append(queue, target)
			}
		}
		next := pc + 1
		switch i.op {
		case codePush:
			if i.constant.kind != intValue && i.constant.kind != boolValue {
				return nil
			}
			push(i.constant.kind)
		case codeLoad:
			push(s.locals[i.slot])
		case codeDeclare, codeStoreLocal:
			s.locals[i.slot] = pop()
		case codeBinary:
			b, a := pop(), pop()
			if a != b || a != intValue && !(a == boolValue && (i.arithmetic == opEqual || i.arithmetic == opNotEqual)) {
				return nil
			}
			if i.arithmetic >= opEqual {
				push(boolValue)
			} else {
				push(intValue)
			}
		case codeUnary:
			k := pop()
			if i.text == "!" && k != boolValue || i.text != "!" && k != intValue {
				return nil
			}
			push(k)
		case codePrint:
			pop()
		case codeCall:
			if len(callees) == 0 {
				return nil
			}
			target := callees[0][i.target]
			if target == nil || target.Receiver != "" || len(target.ParamTypes) != i.count {
				return nil
			}
			for arg := i.count - 1; arg >= 0; arg-- {
				kind := scalarKind(target.ParamTypes[arg])
				if kind == invalidValue || pop() != kind {
					return nil
				}
			}
			if target.Result != "void" {
				push(scalarKind(target.Result))
			}
		case codePop:
			pop()
		case codeEnter:
		case codeExit:
			for _, slot := range i.indices {
				s.locals[slot] = invalidValue
			}
		case codeJump:
			next = i.jump
		case codeJumpFalse:
			if pop() != boolValue {
				return nil
			}
			merge(i.jump, s)
		case codeAnd, codeOr:
			if len(s.stack) == 0 || s.stack[len(s.stack)-1] != boolValue {
				return nil
			}
			merge(i.jump, s)
			pop()
		case codeReturn:
			if i.count != 0 {
				if pop() != scalarKind(source.Result) {
					return nil
				}
			} else if source.Result != "void" {
				return nil
			}
			if !valid || len(s.stack) != 0 {
				return nil
			}
			continue
		default:
			return nil
		}
		if !valid {
			return nil
		}
		merge(next, s)
		if !valid {
			return nil
		}
	}
	// Keep native frames below the Windows stack-probe threshold. Large functions
	// use Go helpers until the JIT can resolve the platform's probe runtime.
	maxStack, maxArgs := 1, max(fn.params, 1)
	for pc, state := range states {
		if state != nil {
			maxStack = max(maxStack, len(state.stack)+1)
			if fn.code[pc].op == codeCall {
				maxArgs = max(maxArgs, fn.code[pc].count)
			}
		}
	}
	if fn.locals+maxStack+maxArgs+16 > 384 {
		return nil
	}
	return states
}

type scalarEmitter struct {
	out        *strings.Builder
	pc, serial int
}

func (e *scalarEmitter) write(format string, args ...any) { fmt.Fprintf(e.out, format, args...) }
func (e *scalarEmitter) temp() string                     { e.serial++; return fmt.Sprintf("%%v%d_%d", e.pc, e.serial) }
func (e *scalarEmitter) address(array string, slot int) string {
	p := e.temp()
	e.write("  %s = getelementptr i64, ptr %%%s, i64 %d\n", p, array, slot)
	return p
}
func (e *scalarEmitter) load(array string, slot int) string {
	p := e.address(array, slot)
	v := e.temp()
	e.write("  %s = load i64, ptr %s\n", v, p)
	return v
}
func (e *scalarEmitter) store(array string, slot int, v string) {
	p := e.address(array, slot)
	e.write("  store i64 %s, ptr %s\n", v, p)
}
func (e *scalarEmitter) callback(action int, value string) string {
	v := e.temp()
	// Direct callees share the outer Go root frame; scalar actions have no PC
	// metadata dependency and use its valid entry PC.
	e.write("  %s = call i64 %%callback(i64 %%frame, i64 0, i64 %d, i64 %s)\n", v, action, value)
	return v
}
func (e *scalarEmitter) checkedCallback(action int, value string, next string) {
	v := e.callback(action, value)
	ok := e.temp()
	e.write("  %s = icmp eq i64 %s, 0\n  br i1 %s, label %%%s, label %%failed\n", ok, v, ok, next)
}

func emitScalarLLVM(out *strings.Builder, fn *preparedFunction, source *Function, symbol string, states []*scalarState, symbols map[*preparedFunction]string) {
	e := &scalarEmitter{out: out, pc: -1}
	maxStack := 1
	for _, s := range states {
		if s != nil && len(s.stack)+1 > maxStack {
			maxStack = len(s.stack) + 1
		}
	}
	locals := max(fn.locals, 1)
	// Public bridge entry roots the host frame once, imports parameters once,
	// and publishes the final result once. Direct guest calls use the internal ABI.
	e.write("\ndefine i64 @%s(i64 %%frame, ptr %%callback, ptr %%budget) {\nentry:\n  %%args = alloca [%d x i64]\n  %%result = alloca i64\n", symbol, max(fn.params, 1))
	e.pc = 0
	for slot := 0; slot < fn.params; slot++ {
		e.store("args", slot, e.callback(jitArgument, fmt.Sprint(slot)))
	}
	e.write("  %%status = call i64 @%s_scalar(i64 %%frame, ptr %%callback, ptr %%budget, ptr %%args, ptr %%result)\n  %%ok = icmp eq i64 %%status, 0\n  br i1 %%ok, label %%publish, label %%failed\npublish:\n", symbol)
	action, v := jitReturnVoid, "0"
	if source.Result != "void" {
		action = jitReturnInt
		if source.Result == "bool" {
			action = jitReturnBool
		}
		v = e.temp()
		e.write("  %s = load i64, ptr %%result\n", v)
	}
	e.checkedCallback(action, v, "returned")
	e.write("returned:\n  ret i64 0\nfailed:\n  ret i64 -1\n}\n")
	e.write("\n; scalar locals, operands and guest calls stay native\ndefine internal i64 @%s_scalar(i64 %%frame, ptr %%callback, ptr %%budget, ptr %%arguments, ptr %%result) {\nentry:\n", symbol)
	maxArgs := 1
	for _, i := range fn.code {
		if i.op == codeCall {
			maxArgs = max(maxArgs, i.count)
		}
	}
	e.write("  %%callargs = alloca [%d x i64]\n  %%callresult = alloca i64\n  %%depthptr = getelementptr i64, ptr %%budget, i64 2\n  %%maxdepthptr = getelementptr i64, ptr %%budget, i64 3\n  %%maxdepth = load i64, ptr %%maxdepthptr\n", maxArgs)
	e.write("  %%locals = alloca [%d x i64]\n  %%stack = alloca [%d x i64]\n  %%limitptr = getelementptr i64, ptr %%budget, i64 1\n  %%limit = load i64, ptr %%limitptr\n", locals, maxStack)
	e.pc = 0
	for slot := 0; slot < fn.params; slot++ {
		v := e.load("arguments", slot)
		e.store("locals", slot, v)
	}
	e.write("  br label %%pc0\n")
	for pc, i := range fn.code {
		e.pc = pc
		e.write("pc%d:\n", pc)
		if states[pc] == nil {
			e.write("  br label %%failed\n")
			continue
		}
		e.write("  %%steps%d = load i64, ptr %%budget\n  %%charged%d = add i64 %%steps%d, 1\n  store i64 %%charged%d, ptr %%budget\n  %%allowed%d = icmp sle i64 %%charged%d, %%limit\n  br i1 %%allowed%d, label %%execute%d, label %%exceeded\nexecute%d:\n", pc, pc, pc, pc, pc, pc, pc, pc, pc)
		h := len(states[pc].stack)
		next := fmt.Sprintf("pc%d", pc+1)
		branch := true
		switch i.op {
		case codePush:
			e.store("stack", h, fmt.Sprint(i.constant.number))
		case codeLoad:
			e.store("stack", h, e.load("locals", i.slot))
		case codeDeclare, codeStoreLocal:
			e.store("locals", i.slot, e.load("stack", h-1))
		case codeBinary:
			a, b := e.load("stack", h-2), e.load("stack", h-1)
			r := e.binary(i.arithmetic, a, b)
			e.store("stack", h-2, r)
		case codeUnary:
			a := e.load("stack", h-1)
			r := e.temp()
			if i.text == "!" {
				e.write("  %s = xor i64 %s, 1\n", r, a)
			} else {
				check := e.temp()
				e.write("  %s = icmp eq i64 %s, -9223372036854775808\n  br i1 %s, label %%overflow, label %%neg%d\nneg%d:\n  %s = sub i64 0, %s\n", check, a, check, pc, pc, r, a)
			}
			e.store("stack", h-1, r)
		case codePrint:
			action := jitPrintInt
			if states[pc].stack[h-1] == boolValue {
				action = jitPrintBool
			}
			e.checkedCallback(action, e.load("stack", h-1), next)
			branch = false
		case codeReturn:
			if i.count != 0 {
				e.write("  store i64 %s, ptr %%result\n", e.load("stack", h-1))
			}
			e.write("  br label %%returned\n")
			branch = false
		case codeCall:
			for arg := 0; arg < i.count; arg++ {
				e.store("callargs", arg, e.load("stack", h-i.count+arg))
			}
			depth, newDepth, allowed := e.temp(), e.temp(), e.temp()
			e.write("  %s = load i64, ptr %%depthptr\n  %s = add i64 %s, 1\n  %s = icmp sle i64 %s, %%maxdepth\n  br i1 %s, label %%call%d, label %%depthExceeded\ncall%d:\n  store i64 %s, ptr %%depthptr\n", depth, newDepth, depth, allowed, newDepth, allowed, pc, pc, newDepth)
			status, ok := e.temp(), e.temp()
			e.write("  %s = call i64 @%s_scalar(i64 %%frame, ptr %%callback, ptr %%budget, ptr %%callargs, ptr %%callresult)\n  store i64 %s, ptr %%depthptr\n  %s = icmp eq i64 %s, 0\n  br i1 %s, label %%callDone%d, label %%failed\ncallDone%d:\n", status, symbols[i.target], depth, ok, status, ok, pc, pc)
			if i.target.result {
				r := e.temp()
				e.write("  %s = load i64, ptr %%callresult\n", r)
				e.store("stack", h-i.count, r)
			}
		case codeJump:
			next = fmt.Sprintf("pc%d", i.jump)
		case codeJumpFalse, codeAnd, codeOr:
			v := e.load("stack", h-1)
			check := e.temp()
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
	e.pc = 0
	for _, trap := range []struct {
		label string
		code  int
	}{{"overflow", 1}, {"zero", 2}, {"exceeded", 4}, {"depthExceeded", 5}} {
		e.write("%s:\n", trap.label)
		e.callback(jitFail, fmt.Sprint(trap.code))
		e.write("  br label %%failed\n")
	}
	e.write("returned:\n  ret i64 0\nfailed:\n  ret i64 -1\n}\n")
}

func (e *scalarEmitter) binary(op arithmeticOp, a, b string) string {
	r := e.temp()
	switch op {
	case opAdd, opSubtract, opMultiply:
		intrinsic := map[arithmeticOp]string{opAdd: "sadd", opSubtract: "ssub", opMultiply: "smul"}[op]
		pair, overflow := e.temp(), e.temp()
		e.write("  %s = call { i64, i1 } @llvm.%s.with.overflow.i64(i64 %s, i64 %s)\n  %s = extractvalue { i64, i1 } %s, 0\n  %s = extractvalue { i64, i1 } %s, 1\n  br i1 %s, label %%overflow, label %%math%d\nmath%d:\n", pair, intrinsic, a, b, r, pair, overflow, pair, overflow, e.pc, e.pc)
	case opDivide, opRemainder:
		zero, min, minus, special := e.temp(), e.temp(), e.temp(), e.temp()
		e.write("  %s = icmp eq i64 %s, 0\n  br i1 %s, label %%zero, label %%divcheck%d\ndivcheck%d:\n  %s = icmp eq i64 %s, -9223372036854775808\n  %s = icmp eq i64 %s, -1\n  %s = and i1 %s, %s\n", zero, b, zero, e.pc, e.pc, min, a, minus, b, special, min, minus)
		if op == opDivide {
			e.write("  br i1 %s, label %%overflow, label %%divide%d\ndivide%d:\n  %s = sdiv i64 %s, %s\n", special, e.pc, e.pc, r, a, b)
		} else {
			rem := e.temp()
			e.write("  br i1 %s, label %%remzero%d, label %%divide%d\nremzero%d:\n  br label %%remjoin%d\ndivide%d:\n  %s = srem i64 %s, %s\n  br label %%remjoin%d\nremjoin%d:\n  %s = phi i64 [ 0, %%remzero%d ], [ %s, %%divide%d ]\n", special, e.pc, e.pc, e.pc, e.pc, e.pc, rem, a, b, e.pc, e.pc, r, e.pc, rem, e.pc)
		}
	default:
		predicate := map[arithmeticOp]string{opEqual: "eq", opNotEqual: "ne", opLess: "slt", opGreater: "sgt", opLessEqual: "sle", opGreaterEqual: "sge"}[op]
		cmp := e.temp()
		e.write("  %s = icmp %s i64 %s, %s\n  %s = zext i1 %s to i64\n", cmp, predicate, a, b, r, cmp)
	}
	return r
}
