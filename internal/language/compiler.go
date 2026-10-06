package language

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var primitives = map[string]bool{"i64": true, "String": true, "bool": true, "void": true}
var builtinVariants = map[string]string{"Some": "Option", "None": "Option", "Ok": "Result", "Err": "Result"}

func parseInteger(text string, token ...Token) int64 {
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		fail("i64 overflow", token...)
	}
	return value
}

type compiler struct {
	types         *typeGraph
	program       *Program
	signatures    map[string]FunctionDecl
	structDecls   map[string]StructDecl
	interfaces    map[string]InterfaceDecl
	methods       map[string]map[string]FunctionDecl
	vars          map[string][]string
	abstract      bool
	symbolicTypes map[string]bool
	building      map[string]bool
}

func (c *compiler) variants(typ string) map[string][]string {
	name, args := c.types.parts(typ)
	if name == "Option" && len(args) == 1 {
		return map[string][]string{"Some": {args[0]}, "None": {}}
	}
	if name == "Result" && len(args) == 2 {
		return map[string][]string{"Ok": {args[0]}, "Err": {args[1]}}
	}
	return c.program.Enums[typ]
}
func (c *compiler) checkType(typ string, allowVoid bool) {
	c.checkTypeRef(c.types.resolve(typ), allowVoid)
}

func (c *compiler) checkTypeRef(ref *typeRef, allowVoid bool) {
	typ := ref.String()
	if typ == "void" && allowVoid {
		return
	}
	if ref == nil {
		fail("Invalid type " + typ)
	}
	name, args := ref.name, ref.args
	if _, ok := c.vars[typ]; ok {
		return
	}
	if ref.isReference() {
		target := ref.target.String()
		if ref.target.isCollection() {
			c.checkTypeRef(ref.target, false)
			return
		}
		if _, ok := c.vars[target]; ok {
			return
		}
		if c.interfaces[target].Name != "" {
			return
		}
		base := ref.target.name
		if _, ok := c.structDecls[base]; ok {
			c.ensureStruct(target)
			return
		}
		fail("Invalid type " + typ)
	}
	if _, ok := c.structDecls[name]; ok {
		c.ensureStruct(typ)
		return
	}
	if name == "Option" || name == "Result" || name == "List" || name == "Map" {
		arity := 1
		if name == "Result" || name == "Map" {
			arity = 2
		}
		if len(args) != arity {
			fail("Invalid type " + typ)
		}
		for _, arg := range args {
			c.checkTypeRef(arg, false)
		}
		if name == "Map" && args[0].String() != "String" && args[0].String() != "i64" && args[0].String() != "bool" {
			fail("Map keys must be String, i64 or bool")
		}
		return
	}
	if typ == "void" || !(primitives[typ] || c.program.Structs[typ] != nil || c.program.Enums[typ] != nil) {
		fail("Invalid type " + typ)
	}
}
func Compile(source string) (program *Program, err error) {
	defer recoverLanguage(&err)
	p := parser{tokens: lex(source)}
	ast := p.program()
	if len(ast.Imports) != 0 {
		fail("Imports require CompileFile or the file-based CLI")
	}
	return compileAST(ast), nil
}

type binding struct {
	Type    *typeRef
	Mutable bool
	Path    []string
}
type functionCompiler struct {
	compiler *compiler
	decl     FunctionDecl
	scopes   []map[string]binding
	fn       *compilerFunction
}

func (f *functionCompiler) lookup(name string, t Token) binding {
	for i := len(f.scopes) - 1; i >= 0; i-- {
		if b, ok := f.scopes[i][name]; ok {
			return b
		}
	}
	fail("Unknown variable "+name, t)
	return binding{}
}
func (f *functionCompiler) declare(name, typ string, mutable bool, t Token) {
	scope := f.scopes[len(f.scopes)-1]
	if builtinVariants[name] != "" {
		fail("Reserved constructor name "+name, t)
	}
	if name == "self" {
		fail("self is reserved for the method receiver", t)
	}
	if _, ok := scope[name]; ok {
		fail("Duplicate variable "+name, t)
	}
	scope[name] = binding{Type: f.compiler.types.resolve(typ), Mutable: mutable}
}
func (f *functionCompiler) requireType(actual, expected string, t Token) {
	if f.compiler.types.resolve(actual) != f.compiler.types.resolve(expected) {
		fail(fmt.Sprintf("Expected %s, got %s", expected, actual), t)
	}
}
func (f *functionCompiler) field(typ, name string, t Token) Field {
	field, ok := f.compiler.program.Structs[f.compiler.types.resolve(typ).base().String()][name]
	if !ok {
		fail("No field "+name+" on "+typ, t)
	}
	return field
}
func (f *functionCompiler) variant(n *Node, expected string) string {
	if len(n.TypeArgs) > 0 {
		fail("Enum constructors do not accept explicit type arguments", n.Token)
	}
	parts := strings.Split(n.Name, "::")
	variant, typ := parts[0], expected
	qualified := len(parts) == 2
	if qualified {
		typ = parts[0]
		variant = parts[1]
	}
	if builtin := builtinVariants[variant]; !qualified && builtin != "" {
		name, _ := f.compiler.types.parts(typ)
		if name != builtin {
			if variant == "Some" && len(n.Args) == 1 {
				payload := f.expr(n.Args[0], "")
				f.compiler.checkType(payload, false)
				typ = "Option<" + payload + ">"
				f.emit(enumInstruction{typ: f.compiler.types.resolve(typ), name: variant, count: 1})
				return typ
			}
			fail(variant+" requires an expected "+builtin+" type; add a type annotation", n.Token)
		}
	}
	types, ok := f.compiler.variants(typ)[variant]
	if !ok {
		fail("Unknown variant "+n.Name, n.Token)
	}
	if len(types) != len(n.Args) {
		fail("Wrong payload count for "+n.Name, n.Token)
	}
	for i, arg := range n.Args {
		f.requireAssignable(f.expr(arg, types[i]), types[i], arg.Token)
	}
	f.emit(enumInstruction{typ: f.compiler.types.resolve(typ), name: variant, count: len(n.Args)})
	return typ
}
func (f *functionCompiler) expr(n *Node, expected string) string {
	switch n.Kind {
	case "literal":
		f.emitLiteral(n)
		return n.Type
	case "variable":
		if builtinVariants[n.Name] != "" || strings.Contains(n.Name, "::") {
			return f.variant(n, expected)
		}
		typ := f.lookup(n.Name, n.Token).Type.String()
		f.emitNamed(irLoad, n.Name)
		return typ
	case "field":
		typ := f.expr(n.Object, "")
		field := f.field(typ, n.Name, n.Token)
		f.emitNamed(irField, n.Name)
		return field.Type
	case "construct":
		return f.structLiteral(n, expected)
	case "reference":
		typ := f.expr(n.Value, "")
		f.emitSimple(irRef)
		return "&" + typ
	case "unary":
		if n.Operator == "-" && n.Value.Kind == "literal" && n.Value.Type == "i64" {
			value := parseInteger("-"+n.Value.Literal.(string), n.Token)
			f.emit(constantInstruction{kind: integerConstant, number: value})
			return "i64"
		}
		typ := f.expr(n.Value, "")
		want := "i64"
		if n.Operator == "!" {
			want = "bool"
		}
		f.requireType(typ, want, n.Token)
		f.emit(unaryInstruction(n.Operator == "!"))
		return typ
	case "binary":
		left := f.expr(n.Left, "")
		if n.Operator == "&&" || n.Operator == "||" {
			f.requireType(left, "bool", n.Token)
			op := irAnd
			if n.Operator == "||" {
				op = irOr
			}
			jump := f.emitJump(op, 0)
			f.requireType(f.expr(n.Right, ""), "bool", n.Token)
			f.patchJump(jump, len(f.fn.code))
			return "bool"
		}
		right := f.expr(n.Right, "")
		f.requireType(right, left, n.Token)
		equality := n.Operator == "==" || n.Operator == "!="
		order := n.Operator == "<" || n.Operator == ">" || n.Operator == "<=" || n.Operator == ">="
		if equality {
			if left != "i64" && left != "bool" && left != "String" {
				fail("Equality currently supports primitive values only", n.Token)
			}
		} else if !(n.Operator == "+" && left == "String") {
			f.requireType(left, "i64", n.Token)
		}
		f.emit(binaryInstruction(arithmeticOps[n.Operator]))
		if equality || order {
			return "bool"
		}
		return left
	case "call":
		if n.Name == "List" || n.Name == "Map" {
			return f.collectionConstructor(n)
		}
		if builtinVariants[n.Name] != "" || strings.Contains(n.Name, "::") {
			return f.variant(n, expected)
		}
		if n.Name == "println" {
			if len(n.TypeArgs) > 0 {
				fail("Function println is not generic", n.Token)
			}
			if len(n.Args) != 1 {
				fail("println takes one argument", n.Token)
			}
			typ := f.expr(n.Args[0], "")
			if typ != "i64" && typ != "String" && typ != "bool" {
				fail("println requires a primitive value", n.Token)
			}
			f.emitSimple(irPrint)
			return "void"
		}
		signature, ok := f.compiler.signatures[n.Name]
		if !ok {
			fail("Unknown function "+n.Name, n.Token)
		}
		if len(signature.Params) != len(n.Args) {
			fail("Wrong argument count for "+n.Name, n.Token)
		}
		if len(signature.Generics) > 0 {
			return f.genericCall(n, signature)
		}
		if len(n.TypeArgs) > 0 {
			fail("Function "+n.Name+" is not generic", n.Token)
		}
		for i, arg := range n.Args {
			f.requireAssignable(f.expr(arg, signature.Params[i].Type), signature.Params[i].Type, arg.Token)
		}
		f.emit(callInstruction{op: irCall, name: n.Name, count: len(n.Args)})
		return signature.Result
	case "method":
		return f.methodCall(n)
	case "try":
		typ := f.expr(n.Value, "")
		source, args := f.compiler.types.parts(typ)
		destination, dargs := f.compiler.types.parts(f.decl.Result)
		if (source != "Option" && source != "Result") || source != destination {
			fail("? requires Option or Result and a matching function return type", n.Token)
		}
		success := "Some"
		if source == "Result" {
			f.requireType(args[1], dargs[1], n.Token)
			success = "Ok"
		}
		f.emit(tryInstruction{typ: f.compiler.types.resolve(f.decl.Result), success: success})
		return args[0]
	case "match":
		typ := f.expr(n.Subject, "")
		variants := f.compiler.variants(typ)
		if variants == nil {
			fail("Cannot match non-enum type "+typ, n.Token)
		}
		seen := map[string]bool{}
		ends := []int{}
		result := expected
		for _, arm := range n.Arms {
			parts := strings.Split(arm.Variant, "::")
			variant := parts[len(parts)-1]
			owner, _ := f.compiler.types.parts(typ)
			if len(parts) == 2 && parts[0] != owner {
				fail("Pattern "+arm.Variant+" does not belong to "+typ, arm.Token)
			}
			payload, ok := variants[variant]
			if !ok {
				fail("Unknown variant "+arm.Variant+" for "+typ, arm.Token)
			}
			if seen[variant] {
				fail("Duplicate match arm "+variant, arm.Token)
			}
			seen[variant] = true
			if len(payload) != len(arm.Bindings) {
				fail("Wrong pattern payload count for "+variant, arm.Token)
			}
			next := f.emit(&branchInstruction{op: irMatch, name: variant})
			f.emitSimple(irEnter)
			f.scopes = append(f.scopes, map[string]binding{})
			for i, name := range arm.Bindings {
				f.emit(payloadInstruction(i))
				if name != "_" {
					f.declare(name, payload[i], false, arm.Token)
					f.emitNamed(irDeclare, name)
				} else {
					f.emitSimple(irPop)
				}
			}
			f.emitSimple(irPop)
			armType := f.expr(arm.Value, result)
			if result == "" {
				result = armType
			} else {
				f.requireAssignable(armType, result, arm.Token)
			}
			f.scopes = f.scopes[:len(f.scopes)-1]
			f.emitSimple(irExit)
			ends = append(ends, f.emitJump(irJump, 0))
			f.patchJump(next, len(f.fn.code))
		}
		missing := []string{}
		for name := range variants {
			if !seen[name] {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			fail("Non-exhaustive match; missing "+strings.Join(missing, ", "), n.Token)
		}
		f.emitNamed(irTrap, "Invalid enum variant")
		for _, end := range ends {
			f.patchJump(end, len(f.fn.code))
		}
		return result
	}
	fail("Unknown expression "+n.Kind, n.Token)
	return ""
}
func (f *functionCompiler) target(n *Node) binding {
	if n.Kind == "variable" {
		b := f.lookup(n.Name, n.Token)
		b.Path = []string{n.Name}
		return b
	}
	if n.Kind == "field" {
		parent := f.target(n.Object)
		field := f.field(parent.Type.String(), n.Name, n.Token)
		return binding{Type: f.compiler.types.resolve(field.Type), Mutable: field.Mutable && (parent.Type.isReference() || parent.Mutable), Path: append(parent.Path, n.Name)}
	}
	fail("Assignment requires a variable or field path", n.Token)
	return binding{}
}
func (f *functionCompiler) block(body []*Node, nested bool) {
	if nested {
		f.scopes = append(f.scopes, map[string]binding{})
		f.emitSimple(irEnter)
	}
	for _, n := range body {
		switch n.Kind {
		case "declare":
			if n.Annotation != "" {
				f.compiler.checkType(n.Annotation, false)
			}
			typ := f.expr(n.Value, n.Annotation)
			if typ == "void" {
				fail("Cannot bind void", n.Token)
			}
			if n.Annotation != "" {
				f.requireAssignable(typ, n.Annotation, n.Token)
				typ = n.Annotation
			}
			f.declare(n.Name, typ, n.Mutable, n.Token)
			f.emitNamed(irDeclare, n.Name)
		case "assign":
			if n.Target.Kind == "variable" && n.Target.Name == "self" {
				fail("Cannot replace self", n.Token)
			}
			destination := f.target(n.Target)
			if !destination.Mutable {
				fail("Cannot assign to const binding or field", n.Token)
			}
			if n.Operator != "=" {
				f.expr(n.Target, "")
			}
			f.requireAssignable(f.expr(n.Value, destination.Type.String()), destination.Type.String(), n.Token)
			if n.Operator != "=" {
				if destination.Type.String() != "i64" && !(destination.Type.String() == "String" && n.Operator == "+=") {
					fail("Invalid compound assignment", n.Token)
				}
				f.emit(binaryInstruction(arithmeticOps[n.Operator[:1]]))
			}
			f.emit(pathInstruction{op: irStore, names: destination.Path})
		case "expression":
			if f.expr(n.Value, "") != "void" {
				f.emitSimple(irPop)
			}
		case "return":
			typ := "void"
			if n.Value != nil {
				typ = f.expr(n.Value, f.decl.Result)
			}
			f.requireAssignable(typ, f.decl.Result, n.Token)
			f.emit(returnInstruction(n.Value != nil))
		case "if":
			f.requireType(f.expr(n.Condition, ""), "bool", n.Token)
			branch := f.emitJump(irJumpFalse, 0)
			f.block(n.Body, true)
			end := f.emitJump(irJump, 0)
			f.patchJump(branch, len(f.fn.code))
			f.block(n.Otherwise, true)
			f.patchJump(end, len(f.fn.code))
		case "while":
			start := len(f.fn.code)
			f.requireType(f.expr(n.Condition, ""), "bool", n.Token)
			end := f.emitJump(irJumpFalse, 0)
			f.block(n.Body, true)
			f.emitJump(irJump, start)
			f.patchJump(end, len(f.fn.code))
		default:
			fail("Unknown statement "+n.Kind, n.Token)
		}
	}
	if nested {
		f.scopes = f.scopes[:len(f.scopes)-1]
		f.emitSimple(irExit)
	}
}
