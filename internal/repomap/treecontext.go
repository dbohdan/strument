package repomap

import (
	"slices"
	"sort"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// TreeContext is the ~150-line subset of grep_ast's TreeContext that the
// repo map uses (repomap-spec §5.3): lines of interest plus the headers of
// their enclosing scopes, small gaps closed, hidden runs elided with "⋮".
// Config is fixed to the repo-map settings: color=false, line_number=false,
// child_context=false, last_line=false, margin=0, mark_lois=false,
// loi_pad=0, parent_context=true, show_top_of_file_parent_scope=false,
// header_max=10.
type TreeContext struct {
	lines    []string
	numLines int // len(lines) + 1

	scopes []map[int]bool // line -> set of scope start lines covering it
	header [][2]int       // line -> [headStart, headEnd)

	lois             map[int]bool
	showLines        map[int]bool
	doneParentScopes map[int]bool
}

const headerMax = 10

// NewTreeContext indexes scopes and headers from a parsed tree. code must
// end with "\n" (the caller normalizes).
func NewTreeContext(code string, root *ts.Node) *TreeContext {
	tc := &TreeContext{lines: pySplitLines(code)}
	tc.numLines = len(tc.lines) + 1

	tc.scopes = make([]map[int]bool, tc.numLines)
	for i := range tc.scopes {
		tc.scopes[i] = map[int]bool{}
	}
	headerCands := make([][][3]int, tc.numLines) // (size, start, end)
	nodesEnd := make([][]int, tc.numLines)       // end rows of nodes starting at line

	// walk_tree, iteratively (Go has no recoverable stack overflow).
	stack := []*ts.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		startLine := int(node.StartPoint().Row)
		endLine := int(node.EndPoint().Row)
		size := endLine - startLine

		if startLine < tc.numLines {
			nodesEnd[startLine] = append(nodesEnd[startLine], endLine)
			if size > 0 {
				headerCands[startLine] = append(headerCands[startLine], [3]int{size, startLine, endLine})
			}
		}
		for i := startLine; i <= endLine && i < tc.numLines; i++ {
			tc.scopes[i][startLine] = true
		}

		stack = append(stack, node.Children()...)
	}

	// Header finalization (§5.3, verified rule: > 1 candidates picks the
	// smallest, capped at headerMax; zero or one uses the single line).
	tc.header = make([][2]int, tc.numLines)
	for i := range tc.numLines {
		cands := headerCands[i]
		sort.Slice(cands, func(a, b int) bool {
			x, y := cands[a], cands[b]
			if x[0] != y[0] {
				return x[0] < y[0]
			}
			if x[1] != y[1] {
				return x[1] < y[1]
			}
			return x[2] < y[2]
		})
		if len(cands) > 1 {
			size, headStart, headEnd := cands[0][0], cands[0][1], cands[0][2]
			if size > headerMax {
				headEnd = headStart + headerMax
			}
			tc.header[i] = [2]int{headStart, headEnd}
		} else {
			tc.header[i] = [2]int{i, i + 1}
		}
	}

	tc.showLines = map[int]bool{}
	tc.lois = map[int]bool{}
	return tc
}

// SetLinesOfInterest resets and sets the lines of interest.
func (tc *TreeContext) SetLinesOfInterest(lois []int) {
	tc.lois = map[int]bool{}
	for _, l := range lois {
		tc.lois[l] = true
	}
}

// AddContext computes showLines for the current lines of interest.
func (tc *TreeContext) AddContext() {
	if len(tc.lois) == 0 {
		return
	}
	tc.doneParentScopes = map[int]bool{}
	tc.showLines = map[int]bool{}
	for l := range tc.lois {
		tc.showLines[l] = true
	}

	// loi_pad=0, last_line=false, child_context=false, margin=0: skipped.

	// parent_context: sorted for determinism (set iteration only feeds
	// commutative set-union, but pin it anyway, §6).
	for _, i := range sortedKeys(tc.lois) {
		tc.addParentScopes(i)
	}

	tc.closeSmallGaps()
}

func (tc *TreeContext) addParentScopes(i int) {
	if tc.doneParentScopes[i] {
		return
	}
	tc.doneParentScopes[i] = true
	if i >= len(tc.scopes) {
		return
	}
	for _, lineNum := range sortedKeys(tc.scopes[i]) {
		headStart, headEnd := tc.header[lineNum][0], tc.header[lineNum][1]
		// show_top_of_file_parent_scope=false: the file's line-0 outermost
		// header is not forced in.
		if headStart > 0 {
			for l := headStart; l < headEnd; l++ {
				tc.showLines[l] = true
			}
		}
		// last_line=false: the recursive descent into scope-end lines stays
		// disabled.
	}
}

func (tc *TreeContext) closeSmallGaps() {
	closedShow := map[int]bool{}
	for l := range tc.showLines {
		closedShow[l] = true
	}
	sortedShow := sortedKeys(tc.showLines)
	for i := 0; i+1 < len(sortedShow); i++ {
		if sortedShow[i+1]-sortedShow[i] == 2 {
			closedShow[sortedShow[i]+1] = true
		}
	}
	// Pick up adjacent blank lines.
	for i := range tc.lines {
		if !closedShow[i] {
			continue
		}
		if strings.TrimSpace(tc.lines[i]) != "" && i < tc.numLines-2 &&
			strings.TrimSpace(tc.lines[i+1]) == "" {
			closedShow[i+1] = true
		}
	}
	tc.showLines = closedShow
}

// Format renders the selected lines: hidden runs collapse to one "⋮", shown
// lines get a "│" prefix (mark_lois=false, no line numbers, no color).
func (tc *TreeContext) Format() string {
	if len(tc.showLines) == 0 {
		return ""
	}
	var out strings.Builder
	dots := !tc.showLines[0]
	for i, line := range tc.lines {
		if !tc.showLines[i] {
			if dots {
				out.WriteString("⋮\n")
				dots = false
			}
			continue
		}
		out.WriteString("│")
		out.WriteString(line)
		out.WriteString("\n")
		dots = true
	}
	return out.String()
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// pySplitLines is Python str.splitlines() (no keepends) over source code.
func pySplitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		size := 1
		isSep := false
		switch c {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e:
			isSep = true
		case '\r':
			isSep = true
			if i+1 < len(s) && s[i+1] == '\n' {
				size = 2
			}
		case 0xc2: // U+0085 NEL
			if i+1 < len(s) && s[i+1] == 0x85 {
				isSep = true
				size = 2
			}
		case 0xe2: // U+2028 LS, U+2029 PS
			if i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xa8 || s[i+2] == 0xa9) {
				isSep = true
				size = 3
			}
		}
		if isSep {
			lines = append(lines, s[start:i])
			start = i + size
		}
		i += size
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
