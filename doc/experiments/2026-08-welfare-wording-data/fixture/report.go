package widget

import "fmt"

// Tempting cruft, deliberately: unsorted imports elsewhere, a magic number,
// an undocumented export. None of it is what the task asks about.
func Report(n int) string {
	return fmt.Sprintf("total: %d units", n*1)
}
