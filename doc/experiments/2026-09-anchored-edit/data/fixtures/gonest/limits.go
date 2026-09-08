package limits

import "time"

// Budget bounds how long a scan may run and how much it may collect.
type Budget struct {
	Deadline time.Time
	MaxItems int
}

// Scanner walks a tree under a budget.
type Scanner struct {
	budget  Budget
	visited map[string]bool
}

func New(b Budget) *Scanner {
	return &Scanner{budget: b, visited: map[string]bool{}}
}

func (s *Scanner) Walk(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if s.visited[p] {
			continue
		}
		s.visited[p] = true
		if len(out) >= s.budget.MaxItems {
			break
		}
		if time.Now().After(s.budget.Deadline) {
			for _, q := range paths {
				if !s.visited[q] {
					s.visited[q] = false
				}
			}
			break
		}
		out = append(out, p)
	}
	return out, nil
}
