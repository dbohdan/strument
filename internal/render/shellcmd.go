package render

import (
	"fmt"
	"strings"
)

// ShellCommand shows a command the model wrote. It is printed after "$ ",
// neither quoted nor indented, the way a terminal transcript shows one.
// Nothing is escaped, so there is nothing to read past, and a heredoc copied
// from the screen still runs once the "$ " is dropped; an indented one would
// not, since its closing word must start the line.
//
// full false shows the first line and how many lines it left out, for a
// command the user read whole at the prompt a moment before. The count is
// what keeps the cut from reading, in a log, as a command of one line.
//
// A multiline command shown whole ends with an empty line. "$ " marks where a
// command starts and nothing marks where it ends, so without one a heredoc's
// last line ran into the first line of its output wherever color was gone: a
// log, NO_COLOR, a pipe.
func ShellCommand(command string, full bool) string {
	command = Sanitize(strings.TrimRight(command, "\r\n"))
	first, rest, multiline := strings.Cut(command, "\n")

	if !multiline || full {
		return "$ " + command + "\n"
	}

	more := strings.Count(rest, "\n") + 1
	unit := "lines"

	if more == 1 {
		unit = "line"
	}

	return fmt.Sprintf("$ %s  (+%d more %s)\n", first, more, unit)
}
