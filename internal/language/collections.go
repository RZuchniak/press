package language

import "strings"

func (f *functionCompiler) collectionConstructor(n *Node) string {
	typ := n.Name + "<" + strings.Join(n.TypeArgs, ",") + ">"
	f.compiler.checkType(typ, false)
	_, args := f.compiler.types.parts(typ)
	if n.Name == "Map" && len(n.Args) != 0 {
		fail("Map constructor takes no arguments", n.Token)
	}
	for _, arg := range n.Args {
		f.requireAssignable(f.expr(arg, args[0]), args[0], arg.Token)
	}
	f.emit(collectionInstruction{kind: parseCollection(n.Name), count: len(n.Args)})
	f.compiler.program.Version = 4
	return typ
}

func (f *functionCompiler) collectionMethod(n *Node, typ string, start int) string {
	name, args := f.compiler.types.parts(f.compiler.types.resolve(typ).base().String())
	kind := parseCollection(name)
	op := resolveCollectionOp(n.Name)
	if op == invalidCollectionOp {
		fail("Unknown collection method "+n.Name, n.Token)
	}
	contract := collectionContracts[op]
	if !contract.supports(kind) {
		required := "Map"
		if contract.list {
			required = "List"
		}
		fail(n.Name+" requires "+required, n.Token)
	}
	var params []string
	result := "void"
	switch op {
	case collectionLen:
		result = "i64"
	case collectionGet:
		if name == "List" {
			params, result = []string{"i64"}, "Option<"+args[0]+">"
		} else {
			params, result = []string{args[0]}, "Option<"+args[1]+">"
		}
	case collectionPush:
		params = []string{args[0]}
	case collectionPop:
		result = "Option<" + args[0] + ">"
	case collectionSet:
		if name == "List" {
			params, result = []string{"i64", args[0]}, "bool"
		} else {
			params, result = args, "Option<"+args[1]+">"
		}
	case collectionRemove:
		params, result = []string{args[0]}, "Option<"+args[1]+">"
	case collectionContains:
		params, result = []string{args[0]}, "bool"
	}
	f.captureReceiver(n, typ, start, contract.mutable)
	if len(n.Args) != contract.arguments {
		fail("Wrong argument count for method "+n.Name, n.Token)
	}
	for i, arg := range n.Args {
		f.requireAssignable(f.expr(arg, params[i]), params[i], arg.Token)
	}
	f.emit(collectionCallInstruction(op))
	f.compiler.program.Version = 4
	return result
}
