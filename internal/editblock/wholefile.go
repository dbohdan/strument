package editblock

import (
	"fmt"
	"path"
	"strings"
)

// WholeFileEdit is one file listing from the "whole" edit format: the new
// content overwrites the file.
type WholeFileEdit struct {
	Path    string
	Content string
	// source ranks filename reliability: "block" (filename line above the
	// fence) > "saw" (chat file quoted in prose) > "chat" (sole chat file).
	source string
}

// ParseWholeFile ports WholeFileCoder.get_edits (mode="update"): fenced
// full-file listings, filename on the line above the fence, with aider's
// fallbacks and the block>saw>chat de-duplication.
func ParseWholeFile(content string, fence Fence, chatFiles []string) ([]WholeFileEdit, error) {
	lines := splitLines(content)

	chatSet := map[string]bool{}
	baseToChat := map[string]string{}
	for _, f := range chatFiles {
		chatSet[f] = true
		baseToChat[path.Base(f)] = f
	}

	var edits []WholeFileEdit
	sawFname := ""
	fname := ""
	fnameSource := ""
	haveBlock := false
	var newLines []string

	for i, line := range lines {
		if strings.HasPrefix(line, fence.Open) || strings.HasPrefix(line, fence.Close) {
			if haveBlock {
				// Ending an existing block.
				sawFname = ""
				edits = append(edits, WholeFileEdit{Path: fname, Content: strings.Join(newLines, ""), source: fnameSource})
				fname, fnameSource = "", ""
				haveBlock = false
				newLines = nil
				continue
			}
			// Starting a new block: the filename is the preceding line.
			if i > 0 {
				fnameSource = "block"
				fname = strings.TrimSpace(lines[i-1])
				fname = strings.Trim(fname, "*") // handle **filename.py**
				fname = strings.TrimRight(fname, ":")
				fname = strings.Trim(fname, "`")
				fname = strings.TrimLeft(fname, "#")
				fname = strings.TrimSpace(fname)
				if len(fname) > 250 { // issue #1232
					fname = ""
				}
				// A bogus path/to prefix from the one-shot example.
				if fname != "" && !chatSet[fname] && chatSet[path.Base(fname)] {
					fname = path.Base(fname)
				}
			}
			if fname == "" {
				switch {
				case sawFname != "":
					fname = sawFname
					fnameSource = "saw"
				case len(chatFiles) == 1:
					fname = chatFiles[0]
					fnameSource = "chat"
				default:
					return nil, fmt.Errorf("No filename provided before %s in file listing", fence.Open)
				}
			}
			haveBlock = true
		} else if haveBlock {
			newLines = append(newLines, line)
		} else {
			// Prose: notice quoted chat-file mentions for the "saw" fallback.
			for _, word := range strings.Fields(strings.TrimSpace(line)) {
				word = strings.TrimRight(word, ".:,;!")
				for _, chatFile := range chatFiles {
					if word == "`"+chatFile+"`" {
						sawFname = chatFile
					}
				}
			}
		}
	}
	if haveBlock && fname != "" {
		edits = append(edits, WholeFileEdit{Path: fname, Content: strings.Join(newLines, ""), source: fnameSource})
	}

	// Most reliable filename source wins per file.
	seen := map[string]bool{}
	var refined []WholeFileEdit
	for _, source := range []string{"block", "saw", "chat"} {
		for _, e := range edits {
			if e.source != source || seen[e.Path] {
				continue
			}
			seen[e.Path] = true
			refined = append(refined, e)
		}
	}
	return refined, nil
}
