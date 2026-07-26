package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// languages holds one *sitter.Language per grammar, built once. tsx is a
// distinct grammar in the typescript module.
var languages = sync.OnceValue(func() map[string]*sitter.Language {
	return map[string]*sitter.Language{
		"go":         sitter.NewLanguage(tsgo.Language()),
		"javascript": sitter.NewLanguage(tsjs.Language()),
		"typescript": sitter.NewLanguage(tsts.LanguageTypescript()),
		"tsx":        sitter.NewLanguage(tsts.LanguageTSX()),
		"python":     sitter.NewLanguage(tspy.Language()),
	}
})

// parseAll fills Symbols and raw import strings for grammar-covered files.
// Parse failures degrade the file to a file-level entry — never an error.
// Raw import strings are temporarily stored in Imports and replaced by
// resolveImports with repo-relative paths.
func parseAll(repoRoot string, files []*File) {
	parser := sitter.NewParser()
	defer parser.Close()
	for _, f := range files {
		lang, ok := languages()[f.Lang]
		if !ok {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(f.Path)))
		if err != nil {
			continue
		}
		if err := parser.SetLanguage(lang); err != nil {
			continue
		}
		tree := parser.Parse(src, nil)
		if tree == nil {
			continue
		}
		root := tree.RootNode()
		switch f.Lang {
		case "go":
			extractGo(root, src, f)
		case "javascript", "typescript", "tsx":
			extractJS(root, src, f)
		case "python":
			extractPython(root, src, f)
		}
		tree.Close()
	}
}

func addSym(f *File, name, kind string, node *sitter.Node) {
	if name == "" {
		return
	}
	f.Symbols = append(f.Symbols, Symbol{Name: name, Kind: kind, Line: int(node.StartPosition().Row) + 1})
}

func fieldText(n *sitter.Node, field string, src []byte) string {
	c := n.ChildByFieldName(field)
	if c == nil {
		return ""
	}
	return c.Utf8Text(src)
}

// --- Go ---

func extractGo(root *sitter.Node, src []byte, f *File) {
	for i := uint(0); i < root.ChildCount(); i++ {
		n := root.Child(i)
		switch n.Kind() {
		case "function_declaration":
			addSym(f, fieldText(n, "name", src), "func", n)
		case "method_declaration":
			name := fieldText(n, "name", src)
			if recv := goReceiverType(n, src); recv != "" {
				name = recv + "." + name
			}
			addSym(f, name, "method", n)
		case "type_declaration":
			for j := uint(0); j < n.NamedChildCount(); j++ {
				spec := n.NamedChild(j)
				if spec.Kind() == "type_spec" || spec.Kind() == "type_alias" {
					addSym(f, fieldText(spec, "name", src), "type", spec)
				}
			}
		case "import_declaration":
			collectGoImports(n, src, f)
		}
	}
}

func goReceiverType(n *sitter.Node, src []byte) string {
	recv := n.ChildByFieldName("receiver")
	if recv == nil || recv.NamedChildCount() == 0 {
		return ""
	}
	t := fieldText(recv.NamedChild(0), "type", src)
	t = strings.TrimPrefix(t, "*")
	if i := strings.IndexByte(t, '['); i > 0 {
		t = t[:i]
	}
	return t
}

func collectGoImports(n *sitter.Node, src []byte, f *File) {
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Kind() == "import_spec" {
			if p := node.ChildByFieldName("path"); p != nil {
				f.Imports = append(f.Imports, strings.Trim(p.Utf8Text(src), "\"`"))
			}
			return
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i))
		}
	}
	walk(n)
}

// --- JavaScript / TypeScript / TSX ---

func extractJS(root *sitter.Node, src []byte, f *File) {
	for i := uint(0); i < root.ChildCount(); i++ {
		n := root.Child(i)
		if n.Kind() == "export_statement" {
			if d := n.ChildByFieldName("declaration"); d != nil {
				n = d
			}
		}
		switch n.Kind() {
		case "function_declaration", "generator_function_declaration", "function_signature":
			addSym(f, fieldText(n, "name", src), "func", n)
		case "class_declaration", "abstract_class_declaration":
			name := fieldText(n, "name", src)
			addSym(f, name, "class", n)
			extractJSMethods(n, src, f, name)
		case "interface_declaration":
			addSym(f, fieldText(n, "name", src), "interface", n)
		case "type_alias_declaration":
			addSym(f, fieldText(n, "name", src), "type", n)
		case "enum_declaration":
			addSym(f, fieldText(n, "name", src), "enum", n)
		case "lexical_declaration", "variable_declaration":
			for j := uint(0); j < n.NamedChildCount(); j++ {
				d := n.NamedChild(j)
				if d.Kind() != "variable_declarator" {
					continue
				}
				if v := d.ChildByFieldName("value"); v != nil {
					switch v.Kind() {
					case "arrow_function", "function_expression", "generator_function":
						addSym(f, fieldText(d, "name", src), "func", d)
					}
				}
			}
		case "import_statement":
			if s := n.ChildByFieldName("source"); s != nil {
				f.Imports = append(f.Imports, strings.Trim(s.Utf8Text(src), "\"'`"))
			}
		}
	}
	collectRequires(root, src, f)
}

func extractJSMethods(class *sitter.Node, src []byte, f *File, className string) {
	body := class.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		if m.Kind() != "method_definition" {
			continue
		}
		name := fieldText(m, "name", src)
		if name == "" || name == "constructor" {
			continue
		}
		addSym(f, className+"."+name, "method", m)
	}
}

// collectRequires walks the whole tree for require("…") calls (CommonJS).
func collectRequires(root *sitter.Node, src []byte, f *File) {
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "call_expression" {
			fn := n.ChildByFieldName("function")
			args := n.ChildByFieldName("arguments")
			if fn != nil && args != nil && fn.Kind() == "identifier" && fn.Utf8Text(src) == "require" && args.NamedChildCount() == 1 {
				a := args.NamedChild(0)
				if a.Kind() == "string" {
					f.Imports = append(f.Imports, strings.Trim(a.Utf8Text(src), "\"'`"))
				}
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
}

// --- Python ---

func extractPython(root *sitter.Node, src []byte, f *File) {
	for i := uint(0); i < root.ChildCount(); i++ {
		n := root.Child(i)
		if n.Kind() == "decorated_definition" {
			if d := n.ChildByFieldName("definition"); d != nil {
				n = d
			}
		}
		switch n.Kind() {
		case "function_definition":
			addSym(f, fieldText(n, "name", src), "func", n)
		case "class_definition":
			name := fieldText(n, "name", src)
			addSym(f, name, "class", n)
			extractPyMethods(n, src, f, name)
		case "import_statement":
			for j := uint(0); j < n.NamedChildCount(); j++ {
				c := n.NamedChild(j)
				switch c.Kind() {
				case "dotted_name":
					f.Imports = append(f.Imports, c.Utf8Text(src))
				case "aliased_import":
					if nm := c.ChildByFieldName("name"); nm != nil {
						f.Imports = append(f.Imports, nm.Utf8Text(src))
					}
				}
			}
		case "import_from_statement":
			if m := n.ChildByFieldName("module_name"); m != nil {
				f.Imports = append(f.Imports, m.Utf8Text(src))
			}
		}
	}
}

func extractPyMethods(class *sitter.Node, src []byte, f *File, className string) {
	body := class.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		if m.Kind() == "decorated_definition" {
			if d := m.ChildByFieldName("definition"); d != nil {
				m = d
			}
		}
		if m.Kind() != "function_definition" {
			continue
		}
		name := fieldText(m, "name", src)
		if name == "" || strings.HasPrefix(name, "__") {
			continue
		}
		addSym(f, className+"."+name, "method", m)
	}
}
