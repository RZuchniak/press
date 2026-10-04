package language

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Instruction arrays preserve the original versioned JSON bytecode contract.
type Instruction []any
type Function struct {
	Params     []string      `json:"params"`
	ParamTypes []string      `json:"paramTypes,omitempty"`
	Result     string        `json:"result"`
	Code       []Instruction `json:"code"`
	Receiver   string        `json:"receiver,omitempty"`
}
type Program struct {
	Format     string                                `json:"format"`
	Version    int                                   `json:"version"`
	Structs    map[string]map[string]Field           `json:"structs"`
	Enums      map[string]map[string][]string        `json:"enums,omitempty"`
	Functions  map[string]*Function                  `json:"functions"`
	Interfaces map[string]map[string]MethodSignature `json:"interfaces,omitempty"`
	Methods    map[string]map[string]string          `json:"methods,omitempty"`
}

var primitives = map[string]bool{"i64": true, "String": true, "bool": true, "void": true}
var builtinVariants = map[string]string{"Some": "Option", "None": "Option", "Ok": "Result", "Err": "Result"}

func typeParts(typ string) (string, []string) {
	start := strings.Index(typ, "<")
	if start < 0 {
		return typ, nil
	}
	args := []string{}
	depth, begin := 0, start+1
	for i := begin; i < len(typ)-1; i++ {
		switch typ[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, typ[begin:i])
				begin = i + 1
			}
		}
	}
	return typ[:start], append(args, typ[begin:len(typ)-1])
}
func parseInteger(text string, token ...Token) int64 {
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		fail("i64 overflow", token...)
	}
	return value
}

type compiler struct {
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
	name, args := typeParts(typ)
	if name == "Option" && len(args) == 1 {
		return map[string][]string{"Some": {args[0]}, "None": {}}
	}
	if name == "Result" && len(args) == 2 {
		return map[string][]string{"Ok": {args[0]}, "Err": {args[1]}}
	}
	return c.program.Enums[typ]
}
func (c *compiler) checkType(typ string, allowVoid bool) {
	if typ == "void" && allowVoid {
		return
	}
	name, args := typeParts(typ)
	if _, ok := c.vars[typ]; ok {
		return
	}
	if strings.HasPrefix(typ, "&") {
		target := typ[1:]
		if _, ok := c.vars[target]; ok {
			return
		}
		if c.interfaces[target].Name != "" {
			return
		}
		base, _ := typeParts(target)
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
	if name == "Option" || name == "Result" {
		arity := 1
		if name == "Result" {
			arity = 2
		}
		if len(args) != arity {
			fail("Invalid type " + typ)
		}
		for _, arg := range args {
			c.checkType(arg, false)
		}
		return
	}
	if typ == "void" || !(primitives[typ] || c.program.Structs[typ] != nil || c.program.Enums[typ] != nil || (strings.HasPrefix(typ, "&") && c.program.Structs[typ[1:]] != nil)) {
		fail("Invalid type " + typ)
	}
}
func Compile(source string) (program *Program, err error) {
	defer recoverLanguage(&err)
	p := parser{tokens: lex(source)}
	return compileAST(p.program()), nil
}

type binding struct {
	Type    string
	Mutable bool
	Path    []string
}
type functionCompiler struct {
	compiler *compiler
	decl     FunctionDecl
	scopes   []map[string]binding
	fn       *Function
}

func (f *functionCompiler) emit(op string, args ...any) int {
	f.fn.Code = append(f.fn.Code, append(Instruction{op}, args...))
	return len(f.fn.Code) - 1
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
	scope[name] = binding{Type: typ, Mutable: mutable}
}
func requireType(actual, expected string, t Token) {
	if actual != expected {
		fail(fmt.Sprintf("Expected %s, got %s", expected, actual), t)
	}
}
func (f *functionCompiler) field(typ, name string, t Token) Field {
	field, ok := f.compiler.program.Structs[strings.TrimPrefix(typ, "&")][name]
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
		name, _ := typeParts(typ)
		if name != builtin {
			if variant == "Some" && len(n.Args) == 1 {
				payload := f.expr(n.Args[0], "")
				f.compiler.checkType(payload, false)
				typ = "Option<" + payload + ">"
				f.emit("ENUM", typ, variant, 1)
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
	f.emit("ENUM", typ, variant, len(n.Args))
	return typ
}
func (f *functionCompiler) expr(n *Node, expected string) string {
	switch n.Kind {
	case "literal":
		if n.Type == "i64" {
			parseInteger(n.Literal.(string), n.Token)
		}
		f.emit("PUSH", n.Type, n.Literal)
		return n.Type
	case "variable":
		if builtinVariants[n.Name] != "" || strings.Contains(n.Name, "::") {
			return f.variant(n, expected)
		}
		typ := f.lookup(n.Name, n.Token).Type
		f.emit("LOAD", n.Name)
		return typ
	case "field":
		typ := f.expr(n.Object, "")
		field := f.field(typ, n.Name, n.Token)
		f.emit("FIELD", n.Name)
		return field.Type
	case "construct":
		return f.structLiteral(n, expected)
	case "reference":
		typ := f.expr(n.Value, "")
		f.emit("REF")
		return "&" + typ
	case "unary":
		if n.Operator == "-" && n.Value.Kind == "literal" && n.Value.Type == "i64" {
			value := parseInteger("-"+n.Value.Literal.(string), n.Token)
			f.emit("PUSH", "i64", strconv.FormatInt(value, 10))
			return "i64"
		}
		typ := f.expr(n.Value, "")
		want := "i64"
		if n.Operator == "!" {
			want = "bool"
		}
		requireType(typ, want, n.Token)
		f.emit("UNARY", n.Operator)
		return typ
	case "binary":
		left := f.expr(n.Left, "")
		if n.Operator == "&&" || n.Operator == "||" {
			requireType(left, "bool", n.Token)
			op := "AND"
			if n.Operator == "||" {
				op = "OR"
			}
			jump := f.emit(op, 0)
			requireType(f.expr(n.Right, ""), "bool", n.Token)
			f.fn.Code[jump][1] = len(f.fn.Code)
			return "bool"
		}
		right := f.expr(n.Right, "")
		requireType(right, left, n.Token)
		equality := n.Operator == "==" || n.Operator == "!="
		order := n.Operator == "<" || n.Operator == ">" || n.Operator == "<=" || n.Operator == ">="
		if equality {
			if left != "i64" && left != "bool" && left != "String" {
				fail("Equality currently supports primitive values only", n.Token)
			}
		} else if !(n.Operator == "+" && left == "String") {
			requireType(left, "i64", n.Token)
		}
		f.emit("BINARY", n.Operator)
		if equality || order {
			return "bool"
		}
		return left
	case "call":
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
			f.emit("PRINT")
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
		f.emit("CALL", n.Name, len(n.Args))
		return signature.Result
	case "method":
		return f.methodCall(n)
	case "try":
		typ := f.expr(n.Value, "")
		source, args := typeParts(typ)
		destination, dargs := typeParts(f.decl.Result)
		if (source != "Option" && source != "Result") || source != destination {
			fail("? requires Option or Result and a matching function return type", n.Token)
		}
		success := "Some"
		if source == "Result" {
			requireType(args[1], dargs[1], n.Token)
			success = "Ok"
		}
		f.emit("TRY", f.decl.Result, success)
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
			owner, _ := typeParts(typ)
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
			next := f.emit("MATCH_VARIANT", variant, 0)
			f.emit("ENTER")
			f.scopes = append(f.scopes, map[string]binding{})
			for i, name := range arm.Bindings {
				f.emit("PAYLOAD", i)
				if name != "_" {
					f.declare(name, payload[i], false, arm.Token)
					f.emit("DECLARE", name)
				} else {
					f.emit("POP")
				}
			}
			f.emit("POP")
			armType := f.expr(arm.Value, result)
			if result == "" {
				result = armType
			} else {
				f.requireAssignable(armType, result, arm.Token)
			}
			f.scopes = f.scopes[:len(f.scopes)-1]
			f.emit("EXIT")
			ends = append(ends, f.emit("JUMP", 0))
			f.fn.Code[next][2] = len(f.fn.Code)
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
		f.emit("TRAP", "Invalid enum variant")
		for _, end := range ends {
			f.fn.Code[end][1] = len(f.fn.Code)
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
		field := f.field(parent.Type, n.Name, n.Token)
		return binding{Type: field.Type, Mutable: field.Mutable && (strings.HasPrefix(parent.Type, "&") || parent.Mutable), Path: append(parent.Path, n.Name)}
	}
	fail("Assignment requires a variable or field path", n.Token)
	return binding{}
}
func (f *functionCompiler) block(body []*Node, nested bool) {
	if nested {
		f.scopes = append(f.scopes, map[string]binding{})
		f.emit("ENTER")
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
			f.emit("DECLARE", n.Name)
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
			f.requireAssignable(f.expr(n.Value, destination.Type), destination.Type, n.Token)
			if n.Operator != "=" {
				if destination.Type != "i64" && !(destination.Type == "String" && n.Operator == "+=") {
					fail("Invalid compound assignment", n.Token)
				}
				f.emit("BINARY", n.Operator[:1])
			}
			f.emit("STORE", destination.Path)
		case "expression":
			if f.expr(n.Value, "") != "void" {
				f.emit("POP")
			}
		case "return":
			typ := "void"
			if n.Value != nil {
				typ = f.expr(n.Value, f.decl.Result)
			}
			f.requireAssignable(typ, f.decl.Result, n.Token)
			f.emit("RETURN", n.Value != nil)
		case "if":
			requireType(f.expr(n.Condition, ""), "bool", n.Token)
			branch := f.emit("JUMP_FALSE", 0)
			f.block(n.Body, true)
			end := f.emit("JUMP", 0)
			f.fn.Code[branch][1] = len(f.fn.Code)
			f.block(n.Otherwise, true)
			f.fn.Code[end][1] = len(f.fn.Code)
		case "while":
			start := len(f.fn.Code)
			requireType(f.expr(n.Condition, ""), "bool", n.Token)
			end := f.emit("JUMP_FALSE", 0)
			f.block(n.Body, true)
			f.emit("JUMP", start)
			f.fn.Code[end][1] = len(f.fn.Code)
		default:
			fail("Unknown statement "+n.Kind, n.Token)
		}
	}
	if nested {
		f.scopes = f.scopes[:len(f.scopes)-1]
		f.emit("EXIT")
	}
}
