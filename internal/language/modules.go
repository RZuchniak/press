package language

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CompileFile resolves explicit imports relative to their declaring file.
// Each file is parsed once; all declarations are linked before type checking.
func CompileFile(path string) (program *Program, err error) {
	defer recoverLanguage(&err)
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	merged := AST{}
	state := map[string]int{}
	var load func(string) error
	load = func(path string) error {
		path = filepath.Clean(path)
		key := path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if state[key] == 1 {
			return fmt.Errorf("Import cycle involving %s", path)
		}
		if state[key] == 2 {
			return nil
		}
		if len(state) >= 256 {
			return fmt.Errorf("Module limit exceeded while loading %s", path)
		}
		state[key] = 1
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("Loading module %s: %w", path, err)
		}
		tokens := lexFile(string(source), path)
		for i := range tokens {
			tokens[i].File = path
		}
		p := parser{tokens: tokens}
		ast := p.program()
		for _, imported := range ast.Imports {
			if filepath.IsAbs(imported) || filepath.VolumeName(imported) != "" {
				return fmt.Errorf("%s: imports must be relative paths", path)
			}
			if err := load(filepath.Join(filepath.Dir(path), imported)); err != nil {
				return err
			}
		}
		merged.Structs = append(merged.Structs, ast.Structs...)
		merged.Enums = append(merged.Enums, ast.Enums...)
		merged.Interfaces = append(merged.Interfaces, ast.Interfaces...)
		merged.Functions = append(merged.Functions, ast.Functions...)
		state[key] = 2
		return nil
	}
	if err := load(root); err != nil {
		return nil, err
	}
	return compileAST(merged), nil
}

func lexFile(source, path string) (tokens []Token) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(languageError); ok {
				panic(languageError{path + ":" + e.message})
			}
			panic(r)
		}
	}()
	return lex(source)
}
