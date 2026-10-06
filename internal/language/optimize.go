package language

// optimizeFunction rewrites prepared instructions while preserving portable PCs
// and the original step budget through fallback instructions.
func optimizeFunction(fn *preparedFunction, targets map[int]bool) {
	// Fuse pure field reads without copying enclosing objects. Preserve original
	// PCs and charge all constituent instructions to the execution budget.
	for pc := range fn.code {
		instruction := &fn.code[pc]
		if instruction.op != codeLoad {
			continue
		}
		for next := pc + 1; next < len(fn.code) && !targets[next] && fn.code[next].op == codeField; next++ {
			instruction.path = append(instruction.path, fn.code[next].path...)
			instruction.span++
		}
		if instruction.span > 1 {
			original := *instruction
			original.span = 1
			original.path = nil
			original.op = codeLoad
			instruction.fallback = &original
			instruction.op = codeReadPath
		}
	}
	// Fuse pure primitive expressions while retaining fallback instructions for
	// exact instruction-budget behavior near a limit.
	for pc := 0; pc+2 < len(fn.code); pc++ {
		left, right, operation := fn.code[pc], fn.code[pc+1], fn.code[pc+2]
		if left.op != codeLoad || right.op != codeLoad && right.op != codePush || operation.op != codeBinary || targets[pc+1] || targets[pc+2] {
			continue
		}
		instruction := &fn.code[pc]
		original := left
		instruction.fallback = &original
		instruction.span = 3
		instruction.op = codeBinaryPush
		instruction.arithmetic = operation.arithmetic
		instruction.rightSlot = right.slot
		instruction.rightConstant = right.op == codePush
		instruction.constant = right.constant
		if pc+3 < len(fn.code) && !targets[pc+3] {
			next := fn.code[pc+3]
			if next.op == codeStoreLocal {
				instruction.op = codeBinaryLocal
				instruction.count = next.slot
				instruction.span = 4
			}
			if next.op == codeJumpFalse {
				instruction.op = codeCompareJump
				instruction.jump = next.jump
				instruction.span = 4
			}
		}
		pc += instruction.span - 1
	}
}
