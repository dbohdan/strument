package render

import "testing"

func TestShellCommand(t *testing.T) {
	heredoc := "python3 - <<'EOF'\nprint(\"it's\")\nEOF"
	for _, c := range []struct {
		command string
		full    bool
		want    string
	}{
		{"echo \"a b\" 'c'", true, "$ echo \"a b\" 'c'\n"},
		{"echo \"a b\" 'c'", false, "$ echo \"a b\" 'c'\n"},
		{heredoc, true, "$ " + heredoc + "\n"},
		{heredoc + "\n", true, "$ " + heredoc + "\n"},
		{heredoc, false, "$ python3 - <<'EOF'  (+2 more lines)\n"},
		{"a\nb", false, "$ a  (+1 more line)\n"},
		// What the model wrote cannot redraw the line being read.
		{"echo hi\x1b[2K\x1b]0;x\x07", true, "$ echo hi\n"},
	} {
		if got := ShellCommand(c.command, c.full); got != c.want {
			t.Errorf("ShellCommand(%q, %v) = %q, want %q", c.command, c.full, got, c.want)
		}
	}
}
