package coder

import "strings"

// pyFormat substitutes Python str.format-style placeholders: {key},
// indexed forms like {fence[0]} (passed as literal keys), and the {{ }}
// escapes. Unknown placeholders are left verbatim (Python would raise;
// the prompt templates are fixed and the slot set is closed, so an
// unknown key here is a template we don't own slots for).
func pyFormat(template string, vars map[string]string) string {
	var b strings.Builder
	i := 0
	for i < len(template) {
		ch := template[i]
		switch ch {
		case '{':
			if i+1 < len(template) && template[i+1] == '{' {
				b.WriteByte('{')
				i += 2
				continue
			}
			end := strings.IndexByte(template[i:], '}')
			if end < 0 {
				b.WriteByte(ch)
				i++
				continue
			}
			key := template[i+1 : i+end]
			if val, ok := vars[key]; ok {
				b.WriteString(val)
			} else {
				b.WriteString(template[i : i+end+1])
			}
			i += end + 1
		case '}':
			if i+1 < len(template) && template[i+1] == '}' {
				b.WriteByte('}')
				i += 2
				continue
			}
			b.WriteByte('}')
			i++
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}
