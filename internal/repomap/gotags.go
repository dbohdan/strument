package repomap

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
)

// goTags extracts a Go file's tags with the compiler's own parser instead of
// the tree-sitter grammar.
//
// Same reasoning as goParseStatus in parse.go, and the same kind of margin:
// extracting tags from this repository's 158 Go files takes tree-sitter 2.78 s
// and go/parser 61 ms — forty-five times — and this is the language Strument is
// written in and most often pointed at. It also knows the enclosing function
// exactly, which the tags alone cannot say: picking the nearest preceding
// definition tag scores 79%, because rule (5) below reports function-local var
// declarations as definitions and the guess lands on a local variable.
//
// What it must not do is *improve* on the grammar. These tags feed the repo
// map's ranking, so the output is held to what queries/go-tags.scm emits, quirks
// and all, and gotags_test.go pins that against tree-sitter over this
// repository. Several rules below look wrong; each is reproducing the query on
// purpose, and says so.
func goTags(relFname, absFname string) []Tag {
	src, err := os.ReadFile(absFname)
	if err != nil || len(src) == 0 {
		return nil
	}
	fset := token.NewFileSet()
	// AllErrors, and the error kept rather than discarded: a file caught
	// mid-edit still yields every declaration before the break, which is the
	// difference between a symbol lookup that degrades while the user types and
	// one that goes blank. SkipObjectResolution because nothing here needs an
	// identifier resolved to its declaration.
	f, err := parser.ParseFile(fset, absFname, src, parser.SkipObjectResolution|parser.AllErrors)
	if f == nil {
		return nil
	}
	g := &goTagger{
		relFname: relFname,
		absFname: absFname,
		fset:     fset,
		src:      src,
		brokenAt: firstGoErrorLine(err),
	}
	g.file(f)
	return g.tags
}

// firstGoErrorLine is the 1-based line of the earliest parse error, or 0 when
// the file parsed cleanly. Named apart from firstErrorLine in parse.go, which
// answers the same question about a tree-sitter tree.
func firstGoErrorLine(err error) int {
	if err == nil {
		return 0
	}
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		line := 0
		for _, e := range list {
			if line == 0 || e.Pos.Line < line {
				line = e.Pos.Line
			}
		}
		return line
	}
	// An error carrying no position. Treat the whole file as suspect rather than
	// none of it: the point of this number is to stop claiming things.
	return 1
}

type goTagger struct {
	relFname string
	absFname string
	fset     *token.FileSet
	src      []byte
	tags     []Tag

	// enclosing is the function whose body is being walked, "" at file scope.
	enclosing string

	// brokenAt is the 1-based line of the first parse error, 0 when the file
	// parsed cleanly. Past it, this file's enclosing names are not trustworthy;
	// see emit.
	brokenAt int
}

func (g *goTagger) emit(kind Kind, name string, pos token.Pos) {
	if name == "" {
		return
	}
	line := g.fset.Position(pos).Line

	// Below a parse error, drop the enclosing name. Recovery does not resume at
	// the right nesting: a half-typed `if x := f(` swallows the remainder of the
	// file into whichever function was open at the break, so every later
	// reference gets confidently attributed to that one. A live pass caught this
	// reporting four of runLS's and runChecks's call sites as runGlob's.
	//
	// Silence is the whole contract here — an annotation is offered as fact, and
	// a wrong function name sends a reader somewhere real and wrong. Above the
	// break the parse was ordinary, so those names stand.
	enclosing := g.enclosing
	if g.brokenAt > 0 && line >= g.brokenAt {
		enclosing = ""
	}

	g.tags = append(g.tags, Tag{
		RelFname:  g.relFname,
		Fname:     g.absFname,
		Line:      line - 1, // Tag.Line is 0-based
		Name:      name,
		Kind:      kind,
		Enclosing: enclosing,
	})
}

// text returns the source between two positions, which is how a tree-sitter
// capture names itself.
func (g *goTagger) text(from, to token.Pos) string {
	lo := g.fset.Position(from).Offset
	hi := g.fset.Position(to).Offset
	if lo < 0 || hi > len(g.src) || lo >= hi {
		return ""
	}
	return string(g.src[lo:hi])
}

func (g *goTagger) file(f *ast.File) {
	// (package_clause "package" (package_identifier) @name.definition.module)
	if f.Name != nil {
		g.emit(Def, f.Name.Name, f.Name.Pos())
	}
	g.walk(f)
}

// walk visits a subtree, handling the nodes that produce tags and letting the
// rest fall through.
//
// Two rules keep this from double-counting, and both are load-bearing. Nothing
// here emits for a bare *ast.Ident — type identifiers come only from typeExpr,
// at the sites where a type actually appears. And **every case that calls
// typeExpr consumes its whole subtree**, walking the non-type parts itself and
// returning false, so the descent can never arrive at the same type expression
// a second time by another route.
func (g *goTagger) walk(n ast.Node) {
	// A slice type has no length expression, and typeExpr passes it through
	// unexamined; ast.Inspect panics on a nil node. Callers holding a typed
	// pointer (a function's body) check it themselves.
	if n == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			g.funcDecl(v) // walks its own subtree, with enclosing set
		case *ast.GenDecl:
			g.genDecl(v) // walks its own specs
		case *ast.CallExpr:
			g.callExpr(v)
		case *ast.CompositeLit:
			g.typeExpr(v.Type)
			for _, e := range v.Elts {
				g.walk(e)
			}
		case *ast.TypeAssertExpr:
			g.typeExpr(v.Type) // nil in a type switch guard; typeExpr ignores it
			g.walk(v.X)
		case *ast.FuncLit:
			g.typeExpr(v.Type)
			if v.Body != nil {
				g.walk(v.Body)
			}
		case *ast.ArrayType, *ast.MapType, *ast.ChanType,
			*ast.StructType, *ast.InterfaceType, *ast.FuncType:
			// A type written where an expression goes: make(chan T, 2),
			// []byte(s), struct{}{}. The grammar labels the identifiers inside
			// these as type identifiers wherever they appear.
			if e, ok := v.(ast.Expr); ok {
				g.typeExpr(e)
			}
		case *ast.TypeSwitchStmt:
			g.typeSwitch(v)
		default:
			return true
		}
		return false
	})
}

// typeSwitch walks a type switch, whose case lists hold types where an ordinary
// switch's hold values. Only the descent knows which of the two it is in, which
// is why this cannot live at *ast.CaseClause.
func (g *goTagger) typeSwitch(s *ast.TypeSwitchStmt) {
	if s.Init != nil {
		g.walk(s.Init)
	}
	g.walk(s.Assign)
	if s.Body == nil {
		return
	}
	for _, stmt := range s.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			g.walk(stmt)
			continue
		}
		for _, e := range cc.List {
			g.typeExpr(e)
		}
		for _, body := range cc.Body {
			g.walk(body)
		}
	}
}

// funcDecl handles (function_declaration) and (method_declaration).
func (g *goTagger) funcDecl(d *ast.FuncDecl) {
	if d.Name != nil {
		// The query captures a method's (field_identifier), so a method is
		// tagged under its own name with no receiver on it.
		g.emit(Def, d.Name.Name, d.Name.Pos())
	}

	prev := g.enclosing
	g.enclosing = funcName(d)
	g.fieldList(d.Recv)
	g.typeExpr(d.Type) // type parameters, then parameters and results
	if d.Body != nil {
		g.walk(d.Body)
	}
	g.enclosing = prev
}

// funcName is how a function is named when something inside it is reported.
// Methods carry their receiver type, because "Push" alone does not locate
// anything in a project with several of them.
func funcName(d *ast.FuncDecl) string {
	if d.Name == nil {
		return ""
	}
	if d.Recv != nil && len(d.Recv.List) > 0 {
		if recv := baseTypeName(d.Recv.List[0].Type); recv != "" {
			return recv + "." + d.Name.Name
		}
	}
	return d.Name.Name
}

// baseTypeName strips a receiver down to its type's name: *List[T] is List.
func baseTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return baseTypeName(t.X)
	case *ast.IndexExpr:
		return baseTypeName(t.X)
	case *ast.IndexListExpr:
		return baseTypeName(t.X)
	}
	return ""
}

func (g *goTagger) genDecl(d *ast.GenDecl) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.ImportSpec:
			// (import_declaration (import_spec) @name.reference.module) wants the
			// spec as a *direct* child of the declaration, which happens only for
			// an unparenthesized `import "fmt"`. A parenthesized block nests its
			// specs one level deeper and matches nothing at all. Reproduced
			// rather than fixed: the ranker was tuned against these tags.
			if !d.Lparen.IsValid() {
				g.emit(Ref, g.text(s.Pos(), s.End()), s.Pos())
			}

		case *ast.TypeSpec:
			// An alias — `type Alias = string` — is a type_alias node in the
			// grammar, not a type_spec, so none of the definition rules reach
			// it and it is defined nowhere. Only the bare type_identifier rule
			// below fires. Reproduced, not fixed.
			if !s.Assign.IsValid() {
				// (type_spec name: (type_identifier) @name.definition.type)
				g.emit(Def, s.Name.Name, s.Name.Pos())
				// A struct or an interface matches a second rule as well
				// (@name.definition.class / @name.definition.interface), so its
				// name is tagged twice. Other type declarations are tagged once.
				switch s.Type.(type) {
				case *ast.StructType, *ast.InterfaceType:
					g.emit(Def, s.Name.Name, s.Name.Pos())
				}
			}
			// (type_identifier) @name.reference.type matches anywhere, including
			// the name being declared, so a declaration is also a reference to
			// itself.
			g.emit(Ref, s.Name.Name, s.Name.Pos())
			g.fieldList(s.TypeParams)
			g.typeExpr(s.Type)

		case *ast.ValueSpec:
			// var and const at any nesting: the query anchors neither to the
			// file, so a var inside a function body is a definition too.
			//
			// A parenthesized `var ( … )` block defines nothing, and a
			// parenthesized `const ( … )` block defines everything. That is not
			// a typo. The grammar wraps grouped var specs in a var_spec_list,
			// putting them out of reach of a rule that wants var_spec as a
			// direct child, while grouped const specs stay direct children of
			// const_declaration. Same shape in the source, different tree — and
			// the same rule that silences a parenthesized import above.
			if d.Tok != token.VAR || !d.Lparen.IsValid() {
				for _, name := range s.Names {
					g.emit(Def, name.Name, name.Pos())
				}
			}
			g.typeExpr(s.Type)
			for _, v := range s.Values {
				g.walk(v)
			}
		}
	}
}

// callExpr handles (call_expression function: ...), which names either a plain
// identifier or the field of a selector — never the package qualifier.
func (g *goTagger) callExpr(c *ast.CallExpr) {
	fun := c.Fun
	parenthesized := false
	for {
		p, ok := fun.(*ast.ParenExpr)
		if !ok {
			break
		}
		fun = p.X // (parenthesized_expression (identifier)) in the query
		parenthesized = true
	}
	switch t := fun.(type) {
	case *ast.Ident:
		g.emit(Ref, t.Name, t.Pos())
	case *ast.SelectorExpr:
		g.emit(Ref, t.Sel.Name, t.Sel.Pos())
		g.walk(t.X) // the receiver may be a call of its own: f().Method()
	case *ast.IndexExpr, *ast.IndexListExpr:
		// An instantiated generic function, NewBuffer[int](…). The grammar reads
		// the whole of it as a generic type, so both the function's name and its
		// type arguments come back as type identifiers rather than as a call.
		g.typeExpr(fun)
	default:
		// Unparenthesized, a conversion's target is written as a type —
		// []byte(s) — and the identifiers in it are type identifiers. Inside
		// parentheses the grammar sees a parenthesized_expression holding
		// ordinary expressions, and the query's two paren rules cover only a
		// bare identifier and a selector, so (*Impl)(nil) contributes nothing.
		if !parenthesized {
			g.walk(fun)
		}
	}
	for _, a := range c.Args {
		g.walk(a)
	}
}

// fieldList walks the types in a parameter, result, receiver, or struct field
// list. Field *names* are not tagged — the Go query has no rule for them.
func (g *goTagger) fieldList(fl *ast.FieldList) {
	if fl == nil {
		return
	}
	for _, f := range fl.List {
		g.typeExpr(f.Type)
	}
}

// typeExpr emits a reference for every (type_identifier) inside a type
// expression. This is the one rule with real reach — the query matches
// type_identifier wherever the grammar labels one — so every place a type can
// appear has to route through here.
func (g *goTagger) typeExpr(e ast.Expr) {
	switch t := e.(type) {
	case nil:
		return
	case *ast.Ident:
		// Including "nil", which `case nil:` in a type switch puts in type
		// position and the grammar labels as a type identifier like any other.
		g.emit(Ref, t.Name, t.Pos())
	case *ast.SelectorExpr:
		// A qualified type: the grammar splits pkg.Type into a
		// package_identifier and a type_identifier, and captures only the
		// second.
		g.emit(Ref, t.Sel.Name, t.Sel.Pos())
	case *ast.StarExpr:
		g.typeExpr(t.X)
	case *ast.ParenExpr:
		g.typeExpr(t.X)
	case *ast.Ellipsis:
		g.typeExpr(t.Elt)
	case *ast.ArrayType:
		g.walk(t.Len) // a length is an ordinary expression, not a type
		g.typeExpr(t.Elt)
	case *ast.MapType:
		g.typeExpr(t.Key)
		g.typeExpr(t.Value)
	case *ast.ChanType:
		g.typeExpr(t.Value)
	case *ast.FuncType:
		g.fieldList(t.TypeParams)
		g.fieldList(t.Params)
		g.fieldList(t.Results)
	case *ast.StructType:
		g.fieldList(t.Fields)
	case *ast.InterfaceType:
		g.fieldList(t.Methods)
	case *ast.IndexExpr:
		// A generic instantiation, List[int].
		g.typeExpr(t.X)
		g.typeExpr(t.Index)
	case *ast.IndexListExpr:
		g.typeExpr(t.X)
		for _, idx := range t.Indices {
			g.typeExpr(idx)
		}
	case *ast.BinaryExpr:
		// A constraint union, ~int | ~string.
		g.typeExpr(t.X)
		g.typeExpr(t.Y)
	case *ast.UnaryExpr:
		g.typeExpr(t.X) // ~int
	}
}
