package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/repomap"
	"dbohdan.com/strument/internal/workspace"
)

// toolCmd runs one observation tool and prints what a model would receive.
//
// It exists because the question "what does this tool actually return, and how
// big is it?" kept being answered by writing a throwaway Go test inside
// internal/workspace, running it, and deleting it. The capability was there all
// along; only the door was missing. `strument tool grep --mode content pat |
// wc -c` is now the answer, and the caps are checkable from a shell script
// rather than from memory.
//
// The tools it offers are the ones that only look. edit, write, bash, and
// verify are deliberately absent: they change files or run processes, they need
// the confirmation machinery a chat session provides, and putting them on a
// command line is a different feature with a different risk profile.
type toolCmd struct {
	Root string `help:"Project root. Defaults to the git worktree root, or the working directory." type:"path"`
	JSON bool   `help:"Print {tool, arguments, result, bytes} instead of the bare result."         name:"json"`

	Read   toolReadCmd   `cmd:"" help:"Read a window of a file, as the read tool returns it."`
	Grep   toolGrepCmd   `cmd:"" help:"Search file contents, as the grep tool returns it."`
	Glob   toolGlobCmd   `cmd:"" help:"Match files by path pattern, as the glob tool returns it."`
	Ls     toolLsCmd     `cmd:"" help:"List a directory, as the ls tool returns it."`
	Symbol toolSymbolCmd `cmd:"" help:"Look a name up in the language parser, as the symbol tool returns it."`
}

// toolStderr carries the one-line outcome — "Searched for … — 100 matches in 5
// files" — to stderr, so stdout holds nothing but the tool's answer and `| wc
// -c` measures what the model would actually be sent.
type toolStderr struct{}

func (toolStderr) Toolf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// inspector builds the tool layer over the project root.
//
// It deliberately does not load the config. The limits these tools obey are
// constants in internal/workspace, and making a read-only search wait on
// `strument trust` would be friction for nothing. If those limits ever become
// config settings, this has to start loading it or the command will quietly
// measure the defaults instead of the project's.
func (c *toolCmd) inspector() (*coder.Inspector, error) {
	root := c.Root
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = cwd
		// The same root the chat session would use, so a measurement taken here
		// describes the tree a turn would see.
		if g, err := gitrepo.Discover(root); err == nil {
			root = g.Root()
		}
	}
	return &coder.Inspector{
		Root:  root,
		Files: workspace.New(root),
		// Always built, unlike in a session: the per-model repo_map setting is
		// about what a prompt carries, and has nothing to say about a lookup
		// somebody asked for directly.
		RepoMap: repomap.New(root),
		Out:     toolStderr{},
	}, nil
}

// run is every subcommand's whole body: build the argument JSON the model would
// send, hand the pair to the Inspector, print what came back.
//
// The exit status is 0 whenever a tool answered, refusals included — "The
// search pattern was not valid: …" is what a model receives for a bad regexp,
// and printing it while exiting non-zero would be this command inventing a
// distinction the harness does not make. Only a broken invocation or an
// unreachable root fails.
func (c *toolCmd) run(name string, args map[string]any) error {
	insp, err := c.inspector()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return err
	}
	result := insp.Run(name, string(encoded))

	if c.JSON {
		// The same string the text path prints, wrapped rather than re-rendered:
		// one rendering means the two cannot drift. Counts a script might want —
		// matches, truncation, shortening — are already sentences inside result.
		return json.NewEncoder(os.Stdout).Encode(struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
			Result    string         `json:"result"`
			Bytes     int            `json:"bytes"`
		}{name, args, result, len(result)})
	}
	if _, err := os.Stdout.WriteString(result); err != nil {
		return err
	}
	// Some results have no trailing newline — the short refusals especially —
	// and one running into the shell prompt is a papercut. Adding one only for
	// a terminal keeps a pipe byte-exact, which is the whole point of `| wc -c`.
	if !strings.HasSuffix(result, "\n") && isCharDevice(os.Stdout) {
		_, err := os.Stdout.WriteString("\n")
		return err
	}
	return nil
}

func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type toolReadCmd struct {
	Path   string `arg:""                                                          help:"File to read, relative to the project root."`
	Offset int    `help:"First line to return (1-based). 0 starts at the top."`
	Limit  int    `help:"How many lines to return. 0 uses the tool's own default."`
}

func (t *toolReadCmd) Run(c *toolCmd) error {
	return c.run("read", map[string]any{"path": t.Path, "offset": t.Offset, "limit": t.Limit})
}

type toolGrepCmd struct {
	Pattern    string `arg:""                                                                                           help:"A Go regular expression."`
	Glob       string `help:"Only search paths matching this glob. Matched against the whole path, so use \"**/*.go\"."`
	Path       string `help:"Only search under this directory."`
	Mode       string `default:"files"                                                                                  enum:"files,content,count"      help:"What to return: the files that match, the matching lines, or a per-file count."`
	IgnoreCase bool   `help:"Match case-insensitively."                                                                 name:"ignore-case"`
}

func (t *toolGrepCmd) Run(c *toolCmd) error {
	return c.run("grep", map[string]any{
		"pattern": t.Pattern, "glob": t.Glob, "path": t.Path,
		"mode": t.Mode, "ignore_case": t.IgnoreCase,
	})
}

type toolGlobCmd struct {
	Pattern string `arg:"" help:"A glob such as \"**/*.go\". \"*.go\" matches only the project root."`
}

func (t *toolGlobCmd) Run(c *toolCmd) error {
	return c.run("glob", map[string]any{"pattern": t.Pattern})
}

type toolLsCmd struct {
	Path string `arg:"" default:"" help:"Directory to list. Empty lists the project root."`
}

func (t *toolLsCmd) Run(c *toolCmd) error {
	return c.run("ls", map[string]any{"path": t.Path})
}

type toolSymbolCmd struct {
	Name string `arg:""               help:"The exact identifier, not a pattern."`
	Kind string `default:"definition" enum:"definition,reference"                 help:"Where the name is declared, or where it is used."`
}

func (t *toolSymbolCmd) Run(c *toolCmd) error {
	return c.run("symbol", map[string]any{"name": t.Name, "kind": t.Kind})
}
