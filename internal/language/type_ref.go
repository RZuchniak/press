package language

import "strings"

// typeRef is an immutable, interned named type or reference. Identity is valid
// within a single compilation; the text is its canonical serialization key.
type typeRef struct {
	name    string
	args    []*typeRef
	argText []string
	target  *typeRef
	text    string
}

func (t *typeRef) String() string {
	if t == nil {
		return ""
	}
	return t.text
}

func (t *typeRef) isReference() bool { return t != nil && t.target != nil }
func (t *typeRef) base() *typeRef {
	if t.isReference() {
		return t.target
	}
	return t
}
func (t *typeRef) isCollection() bool {
	return t != nil && !t.isReference() && parseCollection(t.name) != noCollection
}

type typeGraph struct{ nodes map[string]*typeRef }

func newTypeGraph() *typeGraph { return &typeGraph{nodes: map[string]*typeRef{}} }

func (g *typeGraph) named(name string, args []*typeRef) *typeRef {
	text := name
	var names []string
	if len(args) != 0 {
		names = make([]string, len(args))
		for i, arg := range args {
			names[i] = arg.String()
		}
		text += "<" + strings.Join(names, ",") + ">"
	}
	if existing := g.nodes[text]; existing != nil {
		return existing
	}
	result := &typeRef{name: name, args: append([]*typeRef(nil), args...), argText: names, text: text}
	g.nodes[text] = result
	return result
}

func (g *typeGraph) reference(target *typeRef) *typeRef {
	text := "&" + target.String()
	if existing := g.nodes[text]; existing != nil {
		return existing
	}
	result := &typeRef{target: target, text: text}
	g.nodes[text] = result
	return result
}

func (g *typeGraph) resolve(text string) *typeRef {
	if text == "" {
		return nil
	} // Absent contextual types and method owners.
	if existing := g.nodes[text]; existing != nil {
		return existing
	}
	index := 0
	var parse func() *typeRef
	parse = func() *typeRef {
		if index >= len(text) {
			fail("Invalid type " + text)
		}
		if text[index] == '&' {
			index++
			return g.reference(parse())
		}
		start := index
		for index < len(text) && (text[index] >= 'a' && text[index] <= 'z' || text[index] >= 'A' && text[index] <= 'Z' || text[index] == '_' || index > start && text[index] >= '0' && text[index] <= '9') {
			index++
		}
		if index == start {
			fail("Invalid type " + text)
		}
		name := text[start:index]
		var args []*typeRef
		if index < len(text) && text[index] == '<' {
			index++
			for {
				args = append(args, parse())
				if index >= len(text) {
					fail("Invalid type " + text)
				}
				if text[index] == '>' {
					index++
					break
				}
				if text[index] != ',' {
					fail("Invalid type " + text)
				}
				index++
			}
		}
		return g.named(name, args)
	}
	result := parse()
	if index != len(text) {
		fail("Invalid type " + text)
	}
	return result
}

// parts is an adapter for source/wire declarations that still store type keys.
// Parsing happens once per compilation. Returned argument keys are read-only.
func (g *typeGraph) parts(text string) (string, []string) {
	typ := g.resolve(text)
	if typ == nil {
		return "", nil
	}
	base := typ.base()
	name := base.name
	if typ.isReference() {
		name = "&" + name
	}
	return name, base.argText
}

func (g *typeGraph) substitute(typ *typeRef, replacements map[string]string) *typeRef {
	if typ == nil {
		return nil
	}
	if replacement, ok := replacements[typ.String()]; ok {
		return g.resolve(replacement)
	}
	if typ.isReference() {
		return g.reference(g.substitute(typ.target, replacements))
	}
	if len(typ.args) == 0 {
		return typ
	}
	args := make([]*typeRef, len(typ.args))
	changed := false
	for i, arg := range typ.args {
		args[i] = g.substitute(arg, replacements)
		changed = changed || args[i] != arg
	}
	if !changed {
		return typ
	}
	return g.named(typ.name, args)
}

func (c *compiler) substitute(text string, replacements map[string]string) string {
	return c.types.substitute(c.types.resolve(text), replacements).String()
}

func (c *compiler) infer(pattern, actual string, parameters map[string]bool, replacements map[string]string, token Token) {
	var visit func(*typeRef, *typeRef)
	visit = func(pattern, actual *typeRef) {
		if pattern == nil || actual == nil {
			return
		}
		if parameters[pattern.String()] {
			if previous, ok := replacements[pattern.String()]; ok && c.types.resolve(previous) != actual {
				fail("Conflicting inferred types for "+pattern.String(), token)
			}
			replacements[pattern.String()] = actual.String()
			return
		}
		if pattern.isReference() && actual.isReference() {
			visit(pattern.target, actual.target)
			return
		}
		if pattern.isReference() || actual.isReference() || pattern.name != actual.name || len(pattern.args) != len(actual.args) {
			return
		}
		for i, arg := range pattern.args {
			visit(arg, actual.args[i])
		}
	}
	visit(c.types.resolve(pattern), c.types.resolve(actual))
}
