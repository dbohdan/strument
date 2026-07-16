package repomap

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	ts "github.com/odvcencio/gotreesitter"
)

// parsedFile is the invocation-local parse memo entry (repomap-spec §0.3).
type parsedFile struct {
	src  []byte
	tree *ts.Tree
	lang *langEntry
}

// extractTags ports get_tags_raw over gotreesitter's low-level Query API
// (repomap-spec §1.1): emit one Tag per qualifying capture — no upstream
// double-append — then chroma-backfill refs for definitions-only languages
// (§1.3). Returns nil for unsupported or unparseable files (bare entries).
func extractTags(relFname, absFname string, pf *parsedFile) []Tag {
	if pf == nil || pf.lang == nil || pf.lang.query == nil || pf.tree == nil {
		return nil
	}

	var tags []Tag
	sawDef, sawRef := false, false

	cursor := pf.lang.query.Exec(pf.tree.RootNode(), pf.lang.language, pf.src)
	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		for _, c := range m.Captures {
			var kind Kind
			switch {
			case strings.HasPrefix(c.Name, "name.definition."):
				kind = Def
				sawDef = true
			case strings.HasPrefix(c.Name, "name.reference."):
				kind = Ref
				sawRef = true
			default:
				continue
			}
			if c.Node == nil {
				continue
			}
			tags = append(tags, Tag{
				RelFname: relFname,
				Fname:    absFname,
				Line:     int(c.Node.StartPoint().Row),
				Name:     c.Node.Text(pf.src),
				Kind:     kind,
			})
		}
	}

	if sawRef || !sawDef {
		return tags
	}

	// Defs without refs (e.g. historically C++): backfill references via
	// chroma, marking them line -1 (never a real line).
	lexer := lexers.Match(filepath.Base(absFname))
	if lexer == nil {
		return tags
	}
	it, err := lexer.Tokenise(nil, string(pf.src))
	if err != nil {
		return tags
	}
	for tok := it(); tok != chroma.EOF; tok = it() {
		if isNameToken(tok.Type) {
			tags = append(tags, Tag{
				RelFname: relFname,
				Fname:    absFname,
				Line:     -1,
				Name:     tok.Value,
				Kind:     Ref,
			})
		}
	}
	return tags
}

// isNameToken mirrors pygments' hierarchical `token in Token.Name` check
// (repomap-spec §1.3).
func isNameToken(t chroma.TokenType) bool {
	return t == chroma.Name || strings.HasPrefix(t.String(), "Name.")
}
