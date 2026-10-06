package language

import (
	"sort"
	"strings"
)

type MethodSignature struct {
	Params  []string `json:"params"`
	Result  string   `json:"result"`
	Mutable bool     `json:"mutable"`
}

func signature(method FunctionDecl) MethodSignature {
	result := MethodSignature{Params: []string{}, Result: method.Result, Mutable: method.Mutable}
	for _, p := range method.Params {
		result.Params = append(result.Params, p.Type)
	}
	return result
}
func (c *compiler) equalSignature(a, b FunctionDecl) bool {
	if c.types.resolve(a.Result) != c.types.resolve(b.Result) || a.Mutable != b.Mutable || len(a.Params) != len(b.Params) {
		return false
	}
	for i, p := range a.Params {
		if c.types.resolve(p.Type) != c.types.resolve(b.Params[i].Type) {
			return false
		}
	}
	return true
}
func (c *compiler) rewriteNode(n *Node, replacements map[string]string) *Node {
	if n == nil {
		return nil
	}
	result := *n
	result.Type = c.substitute(n.Type, replacements)
	result.Annotation = c.substitute(n.Annotation, replacements)
	result.TypeArgs = make([]string, len(n.TypeArgs))
	for i, arg := range n.TypeArgs {
		result.TypeArgs[i] = c.substitute(arg, replacements)
	}
	result.Value = c.rewriteNode(n.Value, replacements)
	result.Left = c.rewriteNode(n.Left, replacements)
	result.Right = c.rewriteNode(n.Right, replacements)
	result.Object = c.rewriteNode(n.Object, replacements)
	result.Subject = c.rewriteNode(n.Subject, replacements)
	result.Condition = c.rewriteNode(n.Condition, replacements)
	result.Target = c.rewriteNode(n.Target, replacements)
	result.Args = make([]*Node, len(n.Args))
	for i, arg := range n.Args {
		result.Args[i] = c.rewriteNode(arg, replacements)
	}
	result.Fields = make([]Initializer, len(n.Fields))
	for i, field := range n.Fields {
		result.Fields[i] = Initializer{field.Name, c.rewriteNode(field.Value, replacements)}
	}
	result.Arms = make([]Arm, len(n.Arms))
	for i, arm := range n.Arms {
		result.Arms[i] = arm
		result.Arms[i].Value = c.rewriteNode(arm.Value, replacements)
	}
	result.Body = c.rewriteBody(n.Body, replacements)
	result.Otherwise = c.rewriteBody(n.Otherwise, replacements)
	return &result
}
func (c *compiler) rewriteBody(body []*Node, replacements map[string]string) []*Node {
	result := make([]*Node, len(body))
	for i, n := range body {
		result[i] = c.rewriteNode(n, replacements)
	}
	return result
}
func (c *compiler) specialize(decl FunctionDecl, replacements map[string]string) FunctionDecl {
	result := decl
	result.Result = c.substitute(decl.Result, replacements)
	result.Owner = c.substitute(decl.Owner, replacements)
	result.Generics = nil
	result.Params = make([]Parameter, len(decl.Params))
	for i, p := range decl.Params {
		result.Params[i] = Parameter{p.Name, c.substitute(p.Type, replacements)}
	}
	result.Body = c.rewriteBody(decl.Body, replacements)
	return result
}
func (c *compiler) typeVariables(parameters []TypeParameter) map[string][]string {
	result := map[string][]string{}
	for _, p := range parameters {
		if _, ok := result[p.Name]; ok {
			fail("Duplicate type parameter " + p.Name)
		}
		if p.Name == "self" || primitives[p.Name] || p.Name == "Option" || p.Name == "Result" || c.structDecls[p.Name].Name != "" || c.interfaces[p.Name].Name != "" || c.program.Enums[p.Name] != nil {
			fail("Reserved type parameter " + p.Name)
		}
		seen := map[string]bool{}
		methods := map[string]FunctionDecl{}
		for _, bound := range p.Bounds {
			if c.interfaces[bound].Name == "" {
				fail("Unknown interface bound " + bound)
			}
			if seen[bound] {
				fail("Duplicate interface bound " + bound)
			}
			seen[bound] = true
			for _, method := range c.interfaces[bound].Methods {
				if previous, ok := methods[method.Name]; ok && !c.equalSignature(previous, method) {
					fail("Conflicting interface bounds for method " + method.Name)
				}
				methods[method.Name] = method
			}
		}
		result[p.Name] = p.Bounds
	}
	return result
}
func (c *compiler) satisfies(typ, bound string) bool {
	typ = c.types.resolve(typ).base().String()
	if bounds, ok := c.vars[typ]; ok {
		for _, candidate := range bounds {
			if candidate == bound {
				return true
			}
		}
		return false
	}
	if typ == bound && c.interfaces[typ].Name != "" {
		return true
	}
	base := c.types.resolve(typ).name
	if c.structDecls[base].Name != "" {
		for _, candidate := range c.structDecls[base].Implements {
			if candidate == bound {
				return true
			}
		}
	}
	return false
}
func (c *compiler) checkBounds(parameters []TypeParameter, args []string) map[string]string {
	if len(parameters) != len(args) {
		fail("Wrong type argument count")
	}
	replacements := map[string]string{}
	for i, p := range parameters {
		c.checkType(args[i], false)
		for _, bound := range p.Bounds {
			if !c.satisfies(args[i], bound) {
				fail(args[i] + " does not implement " + bound)
			}
		}
		replacements[p.Name] = args[i]
	}
	return replacements
}
func (c *compiler) symbolic(typ string) bool {
	return c.symbolicRef(c.types.resolve(typ))
}
func (c *compiler) symbolicRef(typ *typeRef) bool {
	if typ == nil {
		return false
	}
	if typ.isReference() {
		return c.symbolicRef(typ.target)
	}
	if _, ok := c.vars[typ.String()]; ok {
		return true
	}
	for _, arg := range typ.args {
		if c.symbolicRef(arg) {
			return true
		}
	}
	return false
}
func (c *compiler) ensureStruct(typ string) {
	name, args := c.types.parts(typ)
	decl, ok := c.structDecls[name]
	if !ok {
		fail("Unknown struct " + typ)
	}
	replacements := c.checkBounds(decl.Generics, args)
	if c.program.Structs[typ] != nil {
		return
	}
	if len(c.program.Structs) > 256 {
		fail("Struct specialization limit exceeded")
	}
	fields := map[string]Field{}
	c.program.Structs[typ] = fields
	c.symbolicTypes[typ] = c.symbolic(typ)
	for _, field := range decl.Fields {
		if _, ok := fields[field.Name]; ok {
			fail("Duplicate field " + field.Name)
		}
		field.Type = c.substitute(field.Type, replacements)
		fields[field.Name] = field
	}
	for _, field := range fields {
		c.checkType(field.Type, false)
	}
	methods := map[string]FunctionDecl{}
	dispatch := map[string]string{}
	for _, method := range decl.Methods {
		if _, ok := methods[method.Name]; ok {
			fail("Duplicate method " + method.Name + " on " + typ)
		}
		if _, ok := fields[method.Name]; ok {
			fail("Field and method share name " + method.Name)
		}
		method.Owner = typ
		method = c.specialize(method, replacements)
		c.validateSignature(method)
		methods[method.Name] = method
		dispatch[method.Name] = typ + "::" + method.Name
	}
	c.methods[typ] = methods
	c.program.Methods[typ] = dispatch
	for _, iface := range decl.Implements {
		requirement, ok := c.interfaces[iface]
		if !ok {
			fail("Unknown interface " + iface)
		}
		for _, required := range requirement.Methods {
			actual, ok := methods[required.Name]
			if !ok {
				fail(typ + " is missing interface method " + iface + "." + required.Name)
			}
			if !c.equalSignature(actual, required) {
				fail("Interface method signature mismatch: " + typ + "." + required.Name)
			}
		}
	}
}
func (c *compiler) validateSignature(decl FunctionDecl) {
	c.checkType(decl.Result, true)
	seen := map[string]bool{}
	for _, p := range decl.Params {
		if p.Name == "self" {
			fail("self is reserved for the method receiver")
		}
		if seen[p.Name] {
			fail("Duplicate variable " + p.Name)
		}
		seen[p.Name] = true
		c.checkType(p.Type, false)
	}
}
func (c *compiler) method(typ, name string, t Token) FunctionDecl {
	base := c.types.resolve(typ).base().String()
	if bounds, ok := c.vars[base]; ok {
		var found *FunctionDecl
		for _, bound := range bounds {
			for _, method := range c.interfaces[bound].Methods {
				if method.Name == name {
					if found != nil && !c.equalSignature(*found, method) {
						fail("Conflicting interface bounds for method "+name, t)
					}
					copy := method
					found = &copy
				}
			}
		}
		if found != nil {
			return *found
		}
		fail("Type parameter "+base+" has no constrained method "+name, t)
	}
	if iface, ok := c.interfaces[base]; ok {
		for _, method := range iface.Methods {
			if method.Name == name {
				return method
			}
		}
		fail("No method "+name+" on "+typ, t)
	}
	c.checkType(typ, false)
	method, ok := c.methods[base][name]
	if !ok {
		fail("No method "+name+" on "+typ, t)
	}
	return method
}
func (c *compiler) assignable(actual, expected string) bool {
	actualRef, expectedRef := c.types.resolve(actual), c.types.resolve(expected)
	if actualRef == expectedRef {
		return true
	}
	if actualRef.isReference() && expectedRef.isReference() && c.interfaces[expectedRef.target.String()].Name != "" {
		return c.satisfies(actual, expectedRef.target.String())
	}
	return false
}
func (f *functionCompiler) requireAssignable(actual, expected string, t Token) {
	if !f.compiler.assignable(actual, expected) {
		f.requireType(actual, expected, t)
	}
}
func (c *compiler) compileDecl(decl FunctionDecl, key string, vars map[string][]string, abstract bool) {
	if !abstract && (c.program.Functions[key] != nil || c.building[key]) {
		return
	}
	if len(c.program.Functions) > 256 {
		fail("Function specialization limit exceeded")
	}
	oldVars, oldAbstract := c.vars, c.abstract
	c.vars, c.abstract = vars, abstract
	defer func() { c.vars, c.abstract = oldVars, oldAbstract }()
	c.validateSignature(decl)
	f := functionCompiler{compiler: c, decl: decl, scopes: []map[string]binding{{}}, fn: &compilerFunction{result: c.types.resolve(decl.Result), receiver: c.types.resolve(decl.Owner)}}
	if !abstract {
		c.building[key] = true
		c.program.Functions[key] = &Function{} // Reserve recursive specializations until their IR is complete.
	}
	if decl.Owner != "" {
		f.scopes[0]["self"] = binding{Type: c.types.resolve(decl.Owner), Mutable: decl.Mutable}
		f.fn.params = append(f.fn.params, "self")
		f.fn.paramTypes = append(f.fn.paramTypes, c.types.resolve(decl.Owner))
	}
	for _, p := range decl.Params {
		f.declare(p.Name, p.Type, false, Token{})
		f.fn.params = append(f.fn.params, p.Name)
		f.fn.paramTypes = append(f.fn.paramTypes, c.types.resolve(p.Type))
	}
	f.block(decl.Body, false)
	if decl.Result != "void" && (len(decl.Body) == 0 || decl.Body[len(decl.Body)-1].Kind != "return") {
		fail(decl.Name + " must end with an explicit return")
	}
	f.emit(returnInstruction(false))
	if !abstract {
		c.program.Functions[key] = serializeFunction(f.fn)
	}
	delete(c.building, key)
}
func (c *compiler) validateLayouts() {
	active, visited := map[*typeRef]bool{}, map[*typeRef]bool{}
	var visit func(*typeRef)
	visit = func(typ *typeRef) {
		if active[typ] {
			fail("Recursive value layout involving " + typ.String() + "; use a reference to break the cycle")
		}
		if visited[typ] || typ.isReference() || typ.isCollection() {
			return
		}
		active[typ] = true
		contents := typ.args
		if fields := c.program.Structs[typ.String()]; fields != nil {
			contents = nil
			for _, field := range fields {
				contents = append(contents, c.types.resolve(field.Type))
			}
		} else if variants := c.program.Enums[typ.String()]; variants != nil {
			contents = nil
			for _, types := range variants {
				for _, typ := range types {
					contents = append(contents, c.types.resolve(typ))
				}
			}
		}
		for _, child := range contents {
			visit(child)
		}
		delete(active, typ)
		visited[typ] = true
	}
	for typ := range c.program.Structs {
		visit(c.types.resolve(typ))
	}
	for typ := range c.program.Enums {
		visit(c.types.resolve(typ))
	}
}
func compileAST(ast AST) *Program {
	c := compiler{types: newTypeGraph(), program: &Program{Format: "press-bytecode", Version: 3, Structs: map[string]map[string]Field{}, Enums: map[string]map[string][]string{}, Functions: map[string]*Function{}, Interfaces: map[string]map[string]MethodSignature{}, Methods: map[string]map[string]string{}}, signatures: map[string]FunctionDecl{}, structDecls: map[string]StructDecl{}, interfaces: map[string]InterfaceDecl{}, methods: map[string]map[string]FunctionDecl{}, vars: map[string][]string{}, symbolicTypes: map[string]bool{}, building: map[string]bool{}}
	names := map[string]bool{"i64": true, "String": true, "bool": true, "void": true, "Option": true, "Result": true, "List": true, "Map": true, "self": true}
	register := func(name string) {
		if names[name] {
			fail("Duplicate/reserved type " + name)
		}
		names[name] = true
	}
	for _, s := range ast.Structs {
		register(s.Name)
		c.structDecls[s.Name] = s
	}
	for _, i := range ast.Interfaces {
		register(i.Name)
		c.interfaces[i.Name] = i
	}
	for _, e := range ast.Enums {
		register(e.Name)
		variants := map[string][]string{}
		for _, v := range e.Variants {
			if _, ok := variants[v.Name]; ok {
				fail("Duplicate variant " + v.Name)
			}
			variants[v.Name] = v.Types
		}
		if len(variants) == 0 {
			fail("Enum " + e.Name + " requires a variant")
		}
		c.program.Enums[e.Name] = variants
	}
	for _, i := range ast.Interfaces {
		methods := map[string]MethodSignature{}
		for _, method := range i.Methods {
			if _, ok := methods[method.Name]; ok {
				fail("Duplicate interface method " + method.Name)
			}
			c.validateSignature(method)
			methods[method.Name] = signature(method)
		}
		c.program.Interfaces[i.Name] = methods
	}
	for _, s := range ast.Structs {
		c.vars = c.typeVariables(s.Generics)
		args := []string{}
		for _, p := range s.Generics {
			args = append(args, p.Name)
		}
		typ := s.Name
		if len(args) > 0 {
			typ += "<" + strings.Join(args, ",") + ">"
		}
		c.ensureStruct(typ)
	}
	c.vars = map[string][]string{}
	for _, e := range c.program.Enums {
		for _, types := range e {
			for _, typ := range types {
				c.checkType(typ, false)
			}
		}
	}
	for _, decl := range ast.Functions {
		if _, ok := c.signatures[decl.Name]; ok || decl.Name == "println" || builtinVariants[decl.Name] != "" || decl.Name == "self" || decl.Name == "List" || decl.Name == "Map" {
			fail("Duplicate/reserved function " + decl.Name)
		}
		c.signatures[decl.Name] = decl
	}
	main, ok := c.signatures["main"]
	if !ok || len(main.Params) != 0 || main.Result != "void" || len(main.Generics) > 0 {
		fail("Program requires fn main() returning void")
	}
	// Check templates using only their declared bounds, even if never instantiated.
	for _, decl := range ast.Functions {
		if len(decl.Generics) > 0 {
			c.compileDecl(decl, decl.Name, c.typeVariables(decl.Generics), true)
		}
	}
	for _, s := range ast.Structs {
		vars := c.typeVariables(s.Generics)
		c.vars = vars
		args := []string{}
		for _, p := range s.Generics {
			args = append(args, p.Name)
		}
		typ := s.Name
		if len(args) > 0 {
			typ += "<" + strings.Join(args, ",") + ">"
		}
		for _, method := range c.methods[typ] {
			c.compileDecl(method, typ+"::"+method.Name, vars, true)
		}
	}
	c.vars = map[string][]string{}
	for _, decl := range ast.Functions {
		if len(decl.Generics) == 0 {
			c.compileDecl(decl, decl.Name, map[string][]string{}, false)
		}
	}
	// Interface calls dispatch dynamically, so emit every method of concrete types.
	for {
		pending := []string{}
		for typ, methods := range c.methods {
			if c.symbolicTypes[typ] {
				continue
			}
			for name := range methods {
				key := typ + "::" + name
				if c.program.Functions[key] == nil {
					pending = append(pending, key)
				}
			}
		}
		if len(pending) == 0 {
			break
		}
		sort.Strings(pending)
		for _, key := range pending {
			parts := strings.Split(key, "::")
			c.compileDecl(c.methods[parts[0]][parts[1]], key, map[string][]string{}, false)
		}
	}
	c.validateLayouts()
	for typ, symbolic := range c.symbolicTypes {
		if symbolic {
			delete(c.program.Structs, typ)
			delete(c.program.Methods, typ)
		}
	}
	return c.program
}

func typeArguments(parameters []TypeParameter, replacements map[string]string, t Token) []string {
	args := []string{}
	for _, p := range parameters {
		typ, ok := replacements[p.Name]
		if !ok {
			fail("Cannot infer "+p.Name+"; supply explicit type arguments", t)
		}
		args = append(args, typ)
	}
	return args
}
func (f *functionCompiler) genericCall(n *Node, decl FunctionDecl) string {
	if len(n.Args) != len(decl.Params) {
		fail("Wrong argument count for "+n.Name, n.Token)
	}
	parameters := map[string]bool{}
	for _, p := range decl.Generics {
		parameters[p.Name] = true
	}
	replacements := map[string]string{}
	if len(n.TypeArgs) > 0 {
		replacements = f.compiler.checkBounds(decl.Generics, n.TypeArgs)
	}
	actuals := []string{}
	for i, arg := range n.Args {
		expected := f.compiler.substitute(decl.Params[i].Type, replacements)
		for name := range parameters {
			if expected == name {
				expected = ""
			}
		}
		if len(n.TypeArgs) == 0 {
			expected = ""
		}
		actual := f.expr(arg, expected)
		actuals = append(actuals, actual)
		if len(n.TypeArgs) == 0 {
			f.compiler.infer(decl.Params[i].Type, actual, parameters, replacements, arg.Token)
		}
	}
	args := typeArguments(decl.Generics, replacements, n.Token)
	replacements = f.compiler.checkBounds(decl.Generics, args)
	specialized := f.compiler.specialize(decl, replacements)
	for i, actual := range actuals {
		f.requireAssignable(actual, specialized.Params[i].Type, n.Args[i].Token)
	}
	key := decl.Name + "<" + strings.Join(args, ",") + ">"
	if !f.compiler.abstract {
		f.compiler.compileDecl(specialized, key, map[string][]string{}, false)
	}
	f.emit(callInstruction{op: irCall, name: key, count: len(n.Args)})
	return specialized.Result
}
func (f *functionCompiler) structLiteral(n *Node, expected string) string {
	decl, ok := f.compiler.structDecls[n.Name]
	if !ok {
		fail("Unknown struct "+n.Name, n.Token)
	}
	typ := n.Name
	args := n.TypeArgs
	if len(decl.Generics) > 0 && len(args) == 0 {
		base, context := f.compiler.types.parts(expected)
		if base == n.Name {
			args = context
		}
	}
	if len(decl.Generics) > 0 && len(args) == 0 {
		parameters := map[string]bool{}
		for _, p := range decl.Generics {
			parameters[p.Name] = true
		}
		replacements := map[string]string{}
		actuals := map[string]string{}
		names := []string{}
		for _, field := range n.Fields {
			if _, ok := actuals[field.Name]; ok {
				fail("Duplicate field "+field.Name, n.Token)
			}
			actual := f.expr(field.Value, "")
			actuals[field.Name] = actual
			names = append(names, field.Name)
			found := false
			for _, definition := range decl.Fields {
				if definition.Name == field.Name {
					f.compiler.infer(definition.Type, actual, parameters, replacements, n.Token)
					found = true
					break
				}
			}
			if !found {
				fail("No field "+field.Name+" on "+n.Name, n.Token)
			}
		}
		args = typeArguments(decl.Generics, replacements, n.Token)
		typ += "<" + strings.Join(args, ",") + ">"
		f.compiler.ensureStruct(typ)
		for name, field := range f.compiler.program.Structs[typ] {
			actual, ok := actuals[name]
			if !ok {
				fail("Missing field "+name, n.Token)
			}
			f.requireAssignable(actual, field.Type, n.Token)
		}
		f.emit(structInstruction{typ: f.compiler.types.resolve(typ), names: names})
		return typ
	}
	if len(args) > 0 {
		typ += "<" + strings.Join(args, ",") + ">"
	}
	f.compiler.ensureStruct(typ)
	fields := f.compiler.program.Structs[typ]
	seen := map[string]bool{}
	names := []string{}
	for _, field := range n.Fields {
		if seen[field.Name] {
			fail("Duplicate field "+field.Name, n.Token)
		}
		seen[field.Name] = true
		want := f.field(typ, field.Name, n.Token).Type
		f.requireAssignable(f.expr(field.Value, want), want, n.Token)
		names = append(names, field.Name)
	}
	for name := range fields {
		if !seen[name] {
			fail("Missing field "+name, n.Token)
		}
	}
	f.emit(structInstruction{typ: f.compiler.types.resolve(typ), names: names})
	return typ
}
func isPath(n *Node) bool { return n.Kind == "variable" || (n.Kind == "field" && isPath(n.Object)) }
func (f *functionCompiler) methodCall(n *Node) string {
	// Capture an lvalue before evaluating arguments so argument effects cannot
	// replace the receiver the call was made on.
	start := len(f.fn.code)
	typ := f.expr(n.Object, "")
	if f.compiler.types.resolve(typ).base().isCollection() {
		return f.collectionMethod(n, typ, start)
	}
	method := f.compiler.method(typ, n.Name, n.Token)
	f.captureReceiver(n, typ, start, method.Mutable)
	if len(n.Args) != len(method.Params) {
		fail("Wrong argument count for method "+n.Name, n.Token)
	}
	for i, arg := range n.Args {
		f.requireAssignable(f.expr(arg, method.Params[i].Type), method.Params[i].Type, arg.Token)
	}
	f.emit(callInstruction{op: irCallMethod, name: n.Name, count: len(n.Args)})
	return method.Result
}

// Both user methods and collection methods capture the same receiver before
// argument evaluation, and apply the same value/reference mutation rules.
func (f *functionCompiler) captureReceiver(n *Node, typ string, start int, mutable bool) {
	if isPath(n.Object) {
		target := f.target(n.Object)
		if mutable && !f.compiler.types.resolve(typ).isReference() && !target.Mutable {
			fail("Cannot call mut method on const receiver", n.Token)
		}
		f.fn.code = f.fn.code[:start]
		f.emit(pathInstruction{op: irLoadPath, names: target.Path})
	} else if mutable && !f.compiler.types.resolve(typ).isReference() {
		fail("Mut methods require a mutable value binding or shared reference", n.Token)
	}
}
