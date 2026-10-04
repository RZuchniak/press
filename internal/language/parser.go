package language

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type languageError struct{ message string }

func (e languageError) Error() string { return e.message }
func fail(message string, token ...Token) {
	if len(token) > 0 && token[0].Line > 0 {
		message = fmt.Sprintf("%d:%d: %s", token[0].Line, token[0].Column, message)
	}
	panic(languageError{message})
}
func recoverLanguage(err *error) {
	if r := recover(); r != nil {
		if e, ok := r.(languageError); ok {
			*err = e
		} else {
			panic(r)
		}
	}
}

type Token struct {
	Text         string
	Line, Column int
}

var tokenPattern = regexp.MustCompile(`^(\s+|//[^\n]*|"(?:[^"\\\n]|\\["\\nrt])*"|\d+|[A-Za-z_][A-Za-z_0-9]*|->|=>|::|\+=|-=|==|!=|<=|>=|&&|\|\||[{}();,:.?&+*/%<>=!\-])`)
var identifier = regexp.MustCompile(`^[A-Za-z_]\w*$`)
var integer = regexp.MustCompile(`^\d+$`)

func lex(source string) []Token {
	tokens := []Token{}
	line, column := 1, 1
	for len(source) > 0 {
		text := tokenPattern.FindString(source)
		if text == "" {
			fail(fmt.Sprintf("Unexpected character %q", source[:1]), Token{Line: line, Column: column})
		}
		if strings.TrimSpace(text) != "" && !strings.HasPrefix(text, "//") {
			tokens = append(tokens, Token{text, line, column})
		}
		for _, char := range text {
			if char == '\n' {
				line++
				column = 1
			} else {
				column++
			}
		}
		source = source[len(text):]
	}
	return append(tokens, Token{"<eof>", line, column})
}

type Node struct {
	Kind, Name, Type, Annotation, Operator                 string
	Literal                                                any
	Mutable                                                bool
	Token                                                  Token
	Value, Left, Right, Object, Subject, Condition, Target *Node
	Args                                                   []*Node
	TypeArgs                                               []string
	Fields                                                 []Initializer
	Arms                                                   []Arm
	Body, Otherwise                                        []*Node
}
type Initializer struct {
	Name  string
	Value *Node
}
type Arm struct {
	Variant  string
	Bindings []string
	Value    *Node
	Token    Token
}
type Field struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Mutable bool   `json:"mutable"`
}
type StructDecl struct {
	Name       string
	Fields     []Field
	Generics   []TypeParameter
	Implements []string
	Methods    []FunctionDecl
}
type TypeParameter struct {
	Name   string
	Bounds []string
}
type InterfaceDecl struct {
	Name    string
	Methods []FunctionDecl
}
type EnumDecl struct {
	Name     string
	Variants []VariantDecl
}
type VariantDecl struct {
	Name  string
	Types []string
}
type Parameter struct{ Name, Type string }
type FunctionDecl struct {
	Name, Result string
	Params       []Parameter
	Body         []*Node
	Generics     []TypeParameter
	Owner        string
	Mutable      bool
}
type AST struct {
	Structs    []StructDecl
	Enums      []EnumDecl
	Functions  []FunctionDecl
	Interfaces []InterfaceDecl
}
type parser struct {
	tokens []Token
	index  int
}

func (p *parser) peek() Token         { return p.tokens[p.index] }
func (p *parser) is(text string) bool { return p.peek().Text == text }
func (p *parser) take() Token {
	t := p.peek()
	if p.index < len(p.tokens)-1 {
		p.index++
	}
	return t
}
func (p *parser) accept(text string) bool {
	if p.is(text) {
		p.take()
		return true
	}
	return false
}
func (p *parser) expect(text string) Token {
	t := p.take()
	if t.Text != text {
		fail(fmt.Sprintf("Expected '%s', got '%s'", text, t.Text), t)
	}
	return t
}
func (p *parser) name() string {
	t := p.take()
	if !identifier.MatchString(t.Text) || strings.Contains("|let|const|fn|struct|enum|match|return|while|if|else|true|false|mut|interface|implements|", "|"+t.Text+"|") {
		fail("Expected identifier", t)
	}
	return t.Text
}
func (p *parser) typ() string {
	ref := p.accept("&")
	name := p.name()
	args := []string{}
	if p.accept("<") {
		for {
			args = append(args, p.typ())
			if !p.accept(",") {
				break
			}
		}
		p.expect(">")
	}
	if len(args) > 0 {
		name += "<" + strings.Join(args, ",") + ">"
	}
	if ref {
		name = "&" + name
	}
	return name
}
func (p *parser) program() AST {
	ast := AST{}
	for !p.is("<eof>") {
		t := p.take()
		switch t.Text {
		case "struct":
			s := StructDecl{Name: p.name()}
			s.Generics = p.generics()
			if p.accept("implements") {
				for {
					s.Implements = append(s.Implements, p.name())
					if !p.accept(",") {
						break
					}
				}
			}
			p.expect("{")
			for !p.accept("}") {
				if p.is("fn") || p.is("mut") {
					s.Methods = append(s.Methods, p.function(true, false))
					continue
				}
				modifier := p.take()
				if modifier.Text != "let" && modifier.Text != "const" {
					fail("Fields require let or const", modifier)
				}
				name := p.name()
				p.expect(":")
				typ := p.typ()
				p.expect(";")
				s.Fields = append(s.Fields, Field{name, typ, modifier.Text == "let"})
			}
			ast.Structs = append(ast.Structs, s)
		case "interface":
			decl := InterfaceDecl{Name: p.name()}
			p.expect("{")
			for !p.accept("}") {
				decl.Methods = append(decl.Methods, p.function(true, true))
			}
			ast.Interfaces = append(ast.Interfaces, decl)
		case "enum":
			e := EnumDecl{Name: p.name()}
			p.expect("{")
			for !p.accept("}") {
				v := VariantDecl{Name: p.name(), Types: []string{}}
				if p.accept("(") {
					if !p.is(")") {
						for {
							v.Types = append(v.Types, p.typ())
							if !p.accept(",") {
								break
							}
						}
					}
					p.expect(")")
				}
				e.Variants = append(e.Variants, v)
				if !p.is("}") {
					p.expect(",")
				}
			}
			ast.Enums = append(ast.Enums, e)
		case "fn":
			p.index--
			f := p.function(false, false)
			ast.Functions = append(ast.Functions, f)
		default:
			fail("Expected struct, enum or fn", t)
		}
	}
	return ast
}
func (p *parser) generics() []TypeParameter {
	params := []TypeParameter{}
	if p.accept("<") {
		for {
			parameter := TypeParameter{Name: p.name()}
			if p.accept(":") {
				for {
					parameter.Bounds = append(parameter.Bounds, p.name())
					if !p.accept("+") {
						break
					}
				}
			}
			params = append(params, parameter)
			if !p.accept(",") {
				break
			}
		}
		p.expect(">")
	}
	return params
}
func (p *parser) function(method, requirement bool) FunctionDecl {
	mutable := p.accept("mut")
	p.expect("fn")
	f := FunctionDecl{Name: p.name(), Result: "void", Params: []Parameter{}, Mutable: mutable}
	f.Generics = p.generics()
	if method && len(f.Generics) > 0 {
		fail("Generic methods are not supported", p.peek())
	}
	p.expect("(")
	if !p.is(")") {
		for {
			name := p.name()
			p.expect(":")
			f.Params = append(f.Params, Parameter{name, p.typ()})
			if !p.accept(",") {
				break
			}
		}
	}
	p.expect(")")
	if p.accept("->") {
		f.Result = p.typ()
	}
	if requirement {
		p.expect(";")
	} else {
		f.Body = p.block()
	}
	return f
}

// Distinguish explicit type arguments from a comparison without changing '<'.
func (p *parser) expressionTypeArgs() (args []string) {
	if !p.is("<") {
		return nil
	}
	start := p.index
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(languageError); !ok {
				panic(r)
			}
			p.index = start
			args = nil
		}
	}()
	p.take()
	for {
		args = append(args, p.typ())
		if !p.accept(",") {
			break
		}
	}
	p.expect(">")
	if !p.is("(") && !p.is("{") {
		p.index = start
		return nil
	}
	return args
}
func (p *parser) block() []*Node {
	p.expect("{")
	body := []*Node{}
	for !p.accept("}") {
		if p.is("<eof>") {
			fail("Unclosed block", p.peek())
		}
		body = append(body, p.statement())
	}
	return body
}
func (p *parser) statement() *Node {
	t := p.peek()
	n := &Node{Token: t}
	if p.accept("let") || p.accept("const") {
		n.Kind = "declare"
		n.Name = p.name()
		n.Mutable = t.Text == "let"
		if p.accept(":") {
			n.Annotation = p.typ()
		}
		p.expect("=")
		n.Value = p.expression(0)
		p.expect(";")
		return n
	}
	if p.accept("return") {
		n.Kind = "return"
		if !p.is(";") {
			n.Value = p.expression(0)
		}
		p.expect(";")
		return n
	}
	if p.accept("while") || p.accept("if") {
		n.Kind = t.Text
		p.expect("(")
		n.Condition = p.expression(0)
		p.expect(")")
		n.Body = p.block()
		if t.Text == "if" && p.accept("else") {
			n.Otherwise = p.block()
		}
		return n
	}
	n.Value = p.expression(0)
	if p.is("=") || p.is("+=") || p.is("-=") {
		n.Kind = "assign"
		n.Target = n.Value
		n.Operator = p.take().Text
		n.Value = p.expression(0)
	} else {
		n.Kind = "expression"
	}
	p.expect(";")
	return n
}

var precedence = map[string]int{"||": 1, "&&": 2, "==": 3, "!=": 3, "<": 4, ">": 4, "<=": 4, ">=": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6}

func (p *parser) expression(min int) *Node {
	left := p.primary()
	for {
		level, ok := precedence[p.peek().Text]
		if !ok || level < min {
			break
		}
		t := p.take()
		left = &Node{Kind: "binary", Left: left, Right: p.expression(level + 1), Operator: t.Text, Token: t}
	}
	return left
}
func (p *parser) primary() *Node {
	t := p.take()
	var value *Node
	if t.Text == "&" {
		value = p.primary()
		if value.Kind != "construct" {
			fail("& currently requires a struct literal", t)
		}
		return &Node{Kind: "reference", Value: value, Token: t}
	}
	if t.Text == "-" || t.Text == "!" {
		return &Node{Kind: "unary", Operator: t.Text, Value: p.primary(), Token: t}
	}
	switch {
	case t.Text == "match":
		p.expect("(")
		subject := p.expression(0)
		p.expect(")")
		p.expect("{")
		arms := []Arm{}
		for !p.accept("}") {
			arm := Arm{Token: p.peek(), Variant: p.name()}
			if p.accept("::") {
				arm.Variant += "::" + p.name()
			}
			if p.accept("(") {
				if !p.is(")") {
					for {
						arm.Bindings = append(arm.Bindings, p.name())
						if !p.accept(",") {
							break
						}
					}
				}
				p.expect(")")
			}
			p.expect("=>")
			arm.Value = p.expression(0)
			arms = append(arms, arm)
			if !p.is("}") {
				p.expect(",")
			}
		}
		value = &Node{Kind: "match", Subject: subject, Arms: arms, Token: t}
	case t.Text == "(":
		value = p.expression(0)
		p.expect(")")
	case integer.MatchString(t.Text):
		value = &Node{Kind: "literal", Type: "i64", Literal: t.Text, Token: t}
	case strings.HasPrefix(t.Text, "\""):
		var s string
		if err := json.Unmarshal([]byte(t.Text), &s); err != nil {
			fail("Invalid string literal", t)
		}
		value = &Node{Kind: "literal", Type: "String", Literal: s, Token: t}
	case t.Text == "true" || t.Text == "false":
		value = &Node{Kind: "literal", Type: "bool", Literal: t.Text == "true", Token: t}
	case identifier.MatchString(t.Text):
		name := t.Text
		if p.accept("::") {
			name += "::" + p.name()
		}
		value = &Node{Name: name, Token: t}
		value.TypeArgs = p.expressionTypeArgs()
		if p.accept("{") {
			value.Kind = "construct"
			if !p.is("}") {
				for {
					name := p.name()
					p.expect(":")
					value.Fields = append(value.Fields, Initializer{name, p.expression(0)})
					if !p.accept(",") || p.is("}") {
						break
					}
				}
			}
			p.expect("}")
		} else if p.accept("(") {
			value.Kind = "call"
			if !p.is(")") {
				for {
					value.Args = append(value.Args, p.expression(0))
					if !p.accept(",") {
						break
					}
				}
			}
			p.expect(")")
		} else {
			value.Kind = "variable"
		}
	default:
		fail("Expected expression", t)
	}
	for {
		if p.accept(".") {
			value = &Node{Kind: "field", Object: value, Name: p.name(), Token: t}
			if p.accept("(") {
				value.Kind = "method"
				if !p.is(")") {
					for {
						value.Args = append(value.Args, p.expression(0))
						if !p.accept(",") {
							break
						}
					}
				}
				p.expect(")")
			}
		} else if p.accept("?") {
			value = &Node{Kind: "try", Value: value, Token: t}
		} else {
			break
		}
	}
	return value
}
