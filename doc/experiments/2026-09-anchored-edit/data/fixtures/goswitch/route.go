package route

import "strings"

// Kind classifies a path.
type Kind int

const (
	Unknown Kind = iota
	Source
	Doc
	Data
)

// Classify sorts paths into kinds, skipping anything hidden.
func Classify(paths []string) map[string]Kind {
	out := map[string]Kind{}
	for _, p := range paths {
		base := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[i+1:]
		}
		if strings.HasPrefix(base, ".") {
			continue
		}
		switch {
		case strings.HasSuffix(base, ".go"), strings.HasSuffix(base, ".py"):
			out[p] = Source
		case strings.HasSuffix(base, ".md"):
			out[p] = Doc
		case strings.HasSuffix(base, ".json"):
			out[p] = Data
		default:
			out[p] = Unknown
		}
	}
	return out
}
