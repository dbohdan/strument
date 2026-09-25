package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/render"
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
// check are deliberately absent: they change files or run processes, they need
// the confirmation machinery a chat session provides, and putting them on a
// command line is a different feature with a different risk profile.
type toolCmd struct {
	Root string `help:"Project root. Defaults to the git worktree root, or the working directory."                       placeholder:"<dir>" type:"path"`
	JSON bool   `help:"Print a JSON object with the tool, arguments, result, and byte count instead of the bare result." name:"json"`

	Read   toolReadCmd   `cmd:"" help:"Read a window of a file, as the read tool returns it."`
	Grep   toolGrepCmd   `cmd:"" help:"Search file contents, as the grep tool returns it."`
	Glob   toolGlobCmd   `cmd:"" help:"Match files by path pattern, as the glob tool returns it."`
	Ls     toolLsCmd     `cmd:"" help:"List a directory, as the ls tool returns it."`
	Symbol toolSymbolCmd `cmd:"" help:"Look a name up in the language parser, as the symbol tool returns it."`
	// RunCode's subcommand name comes from the name tag: the field name cannot
	// carry the underscore (revive), and the tool's exact name — not a
	// kebab-cased reading of it — is the point of a door that shows what the
	// model sees.
	RunCode toolRunCodeCmd `cmd:"" help:"Run a short JavaScript program through run_code, as the model's tool returns it."     name:"run_code"`
	About   toolAboutCmd   `cmd:"" help:"Report this binary's build, the time and the platform, as the about tool returns it."`
}

// toolStderr carries the one-line outcome — "Searched for … — 100 matches in 5
// files" — to stderr, so stdout holds nothing but the tool's answer and `| wc
// -c` measures what the model would actually be sent.
type toolStderr struct{}

func (toolStderr) Toolf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// ToolBlock routes a run_code program's shaped source block to stderr too, on
// the same reasoning: the block is the command's announcement, not its answer.
func (toolStderr) ToolBlock(title, body string) {
	render.ToolBlock(os.Stderr, title, body)
}

// The rest of coder.Output is unreachable from these commands — they never
// stream an answer, print a link, or render a diff — and panics rather than
// staying silent would hide a future runCode path growing one.
func (toolStderr) Printf(string, ...any)              { panic("tool: output used as a full Output") }
func (toolStderr) CommandOutput(string)               { panic("tool: output used as a full Output") }
func (toolStderr) Warningf(string, ...any)            { panic("tool: output used as a full Output") }
func (toolStderr) Errorf(string, ...any)              { panic("tool: output used as a full Output") }
func (toolStderr) Link(string)                        { panic("tool: output used as a full Output") }
func (toolStderr) StreamText(string)                  { panic("tool: output used as a full Output") }
func (toolStderr) StreamReasoning(string)             { panic("tool: output used as a full Output") }
func (toolStderr) StreamToolCall(int, string, string) { panic("tool: output used as a full Output") }
func (toolStderr) FlushStream()                       { panic("tool: output used as a full Output") }

// inspector builds the tool layer over the project root.
//
// It deliberately does not load the config. The limits these tools obey are
// constants in internal/workspace, and making a read-only search wait on
// `strument trust` would be friction for nothing. If those limits ever become
// config settings, this has to start loading it or the command will quietly
// measure the defaults instead of the project's.
func (c *toolCmd) inspector() (*coder.Inspector, error) {
	root, err := c.root()
	if err != nil {
		return nil, err
	}
	return &coder.Inspector{
		Root:  root,
		Files: workspace.New(root),
		// Always built, whatever language_parser says: that setting is about
		// what a session offers the model, and has nothing to say about a
		// lookup somebody asked for directly.
		RepoMap: repomap.New(root),
		Out:     toolStderr{},
	}, nil
}

// coder builds the minimal Coder that run_code needs. The chat session's own
// fields — the staleness tracker, the anchor registry, the model — are nil or
// default here, which every path a program can reach treats as "nothing
// special": a bridge call reads through Files, and nothing a program can do
// writes a file or talks to a model.
func (c *toolCmd) coder() (*coder.Coder, error) {
	root, err := c.root()
	if err != nil {
		return nil, err
	}
	return &coder.Coder{
		Root:    root,
		Out:     toolStderr{},
		Files:   workspace.New(root),
		RepoMap: repomap.New(root),
	}, nil
}

// root resolves --root the same way for every subcommand: the flag, else the
// git worktree root, else the working directory.
func (c *toolCmd) root() (string, error) {
	if c.Root != "" {
		return c.Root, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// The same root the chat session would use, so a measurement taken here
	// describes the tree a turn would see.
	if g, err := gitrepo.Discover(cwd); err == nil {
		return g.Root(), nil
	}
	return cwd, nil
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
	return c.print(name, args, insp.Run(name, string(encoded)))
}

// print writes one tool's result: bare, or wrapped by --json.
func (c *toolCmd) print(name string, args map[string]any, result string) error {
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
	if !strings.HasSuffix(result, "\n") && isTerminal(os.Stdout) {
		_, err := os.Stdout.WriteString("\n")
		return err
	}
	return nil
}

// isTerminal reports whether f is a terminal a person is at.
//
// It asks the terminal driver rather than checking for a character device,
// which is what it did: /dev/null is a character device too, so
// `strument -m … < /dev/null` — the ordinary way to run it from a script or CI
// — was taken for a person at a keyboard. Confirmations were printed and then
// read EOF, and nothing said they had been declined.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

type toolReadCmd struct {
	Path   string `arg:""                                                          help:"File to read, relative to the project root. An absolute path inside the project also works."`
	Offset int    `help:"First line to return (1-based). 0 starts at the top."     placeholder:"<n>"`
	Limit  int    `help:"How many lines to return. 0 uses the tool's own default." placeholder:"<n>"`
	// Outline is the read tool's outline parameter, which a build offers the
	// model only in the read-outline trial's arms C and D. Here it is always
	// accepted, and a build without it reads the file as usual.
	Outline bool `help:"Return the file's definitions and the lines they span instead of its contents."`
}

func (t *toolReadCmd) Run(c *toolCmd) error {
	return c.run("read", map[string]any{"path": t.Path, "offset": t.Offset, "limit": t.Limit, "outline": t.Outline})
}

type toolGrepCmd struct {
	Pattern    string `arg:""                                                                                           help:"A Go regular expression."`
	Glob       string `help:"Only search paths matching this glob. Matched against the whole path, so use \"**/*.go\"." placeholder:"<glob>"`
	Path       string `help:"Only search under this directory."                                                         placeholder:"<dir>"`
	Mode       string `default:"files"                                                                                  enum:"files,content,count"      help:"What to return: the files that match, the matching lines, or a per-file count."`
	IgnoreCase bool   `help:"Match case-insensitively."                                                                 name:"ignore-case"`
	Context    int    `help:"Lines of context to show around each match, like grep -C."                                 name:"context-lines"            placeholder:"<n>"`
}

func (t *toolGrepCmd) Run(c *toolCmd) error {
	return c.run("grep", map[string]any{
		"pattern": t.Pattern, "glob": t.Glob, "path": t.Path,
		"mode": t.Mode, "ignore_case": t.IgnoreCase, "context_lines": t.Context,
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

// toolRunCodeCmd runs a program through the run_code tool. The subcommand name
// is the tool's exact name, not a kebab-cased reading of it: the mapping from
// command to tool is one-to-one here, which is the whole point of a door that
// shows what the model sees.
//
// The program is the arg, read verbatim — it is JavaScript, and a shell will
// eat its own quoting before kong sees it, so the usual invocation is
// `strument tool run_code 'console.log(1 + 2)'` with the program single-quoted.
type toolRunCodeCmd struct {
	Code string `arg:"" help:"The JavaScript program, as the run_code tool would receive it."`
}

func (t *toolRunCodeCmd) Run(c *toolCmd) error {
	cod, err := c.coder()
	if err != nil {
		return err
	}
	// The program block and the outcome line went to stderr, so stdout is the
	// result alone — the same stdout discipline as run(): byte-exact through a
	// pipe, one trailing newline added for a terminal. --json is deliberately
	// not offered here: the result is already data-shaped text, and the other
	// tools' {tool, arguments, result} envelope would measure the envelope.
	result := cod.RunCode(t.Code)
	if _, err := os.Stdout.WriteString(result); err != nil {
		return err
	}
	if !strings.HasSuffix(result, "\n") && isTerminal(os.Stdout) {
		_, err := os.Stdout.WriteString("\n")
		return err
	}
	return nil
}

type toolAboutCmd struct{}

// Run prints the about report. Not through the Inspector, which holds the
// tools that look at the project; this one looks at the process. Its session
// half describes this process too, which has no model and no sandbox.
func (t *toolAboutCmd) Run(c *toolCmd) error {
	cdr, err := c.coder()
	if err != nil {
		return err
	}
	cdr.Build = buildInfo()
	return c.print("about", map[string]any{}, cdr.About(time.Now()))
}
