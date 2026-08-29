package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Discovery: which SKILL.md files this session can see, and which of them the
// user has vouched for.
//
// A skill is instructions that enter the model's context and change how it
// behaves. A project-local one therefore travels with the repository, so
// cloning a hostile project would otherwise be enough to inject them — the
// same shape as a repository's .git/config being able to run commands. The
// answer is the one Strument already uses for a project config: the user says
// `strument trust`, and until they do the skill is found, named, and not used.

const (
	// MaxSkillsPerRoot bounds a directory of junk to a diagnostic rather than
	// a hang. Nobody has this many skills; anybody can create this many files.
	MaxSkillsPerRoot = 256
	// MaxSkillBytes bounds one file. The frontmatter has its own smaller
	// limit; this is the ceiling on what is read from disk at all.
	MaxSkillBytes = 256 * 1024
)

// Scope is where a skill was found, which decides whether it needs trusting.
type Scope int

const (
	// ScopeProject is a skill inside the project directory. It travels with
	// the repository, so it needs the user's explicit trust.
	ScopeProject Scope = iota
	// ScopeGlobal is a skill under the user's own directories. Putting a file
	// there is already a deliberate act by the person the session belongs to.
	ScopeGlobal
)

func (s Scope) String() string {
	if s == ScopeProject {
		return "project"
	}
	return "global"
}

// Root is one directory searched for skills.
type Root struct {
	Path  string
	Scope Scope
}

// Skill is one discovered SKILL.md.
//
// Trusted is the field that matters. An untrusted skill is returned rather
// than hidden, because the user has to be told it exists in order to decide
// about it — but its Body and Description must never reach the model while
// Trusted is false. Use Usable to filter; see the warning there.
type Skill struct {
	Frontmatter

	// Body is the Markdown after the frontmatter: the instructions themselves.
	Body string
	// Path is the SKILL.md; Dir is the directory holding it, which is what
	// relative references inside the skill resolve against.
	//
	// Dir is not guaranteed to sit inside its root: a skills directory may
	// hold a symlink, and following one is the point of putting it there.
	// Nothing here reads anything under Dir, so that is safe as it stands —
	// but whatever eventually resolves a skill's scripts/ or references/ must
	// contain those paths itself rather than trusting Dir to be somewhere in
	// particular.
	Path string
	Dir  string
	Root Root
	// Trusted is true when the skill needs no trust (global) or when the
	// user's trust record matches this file's current content.
	Trusted bool
}

// Diagnostic is a skill that was found and not used, with the reason. Reported
// rather than dropped: a skill silently missing looks identical to a skill
// nobody wrote, and the two call for opposite responses from the user.
type Diagnostic struct {
	Path    string
	Message string
}

// Truster reports whether a file's current content has been trusted.
// *config.TrustStore satisfies it. An interface rather than the concrete type
// so a test can express a trust decision in one line.
type Truster interface {
	IsTrusted(absPath string, content []byte) bool
}

// Options configures discovery. Every path is injectable so tests need no
// home directory and no environment.
type Options struct {
	// ProjectRoot is the directory a session opened. Empty skips project
	// scopes entirely.
	ProjectRoot string
	// Roots overrides the default set. Nil uses DefaultRoots.
	Roots []Root
	// Trust decides project skills. Nil means nothing is trusted, which is the
	// right default for a caller that forgot to pass one.
	Trust Truster
}

// DataDir is $XDG_DATA_HOME/strument, defaulting to ~/.local/share/strument.
//
// Data rather than config, and the distinction is not pedantry: config.star
// configures Strument, while a skills tree is material the user installed —
// instructions with scripts, references and assets beside them, often
// unpacked from a release. XDG calls the first configuration and the second
// data. Strument already separates config, state and cache; this is the fourth
// category, not a fourth convention.
//
// XDG_DATA_HOME is honored on every platform, including macOS, because the
// state directory already behaves that way and one rule beats two.
func DataDir() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "strument"), nil
}

// DefaultRoots is where skills are looked for, in precedence order: project
// before global, and Strument's own directory before the shared one.
//
// .agents/skills is a cross-tool location several harnesses read, so a skill
// dropped there works in more than one of them. It is not Strument's
// directory, which is why no XDG question arises for it.
//
// Deliberately absent: .claude/skills and the other vendors' directories.
// Reading another tool's directory means inheriting its trust assumptions
// along with its files, and that deserves its own decision rather than
// arriving as a side effect of this one.
func DefaultRoots(projectRoot string) ([]Root, error) {
	var roots []Root
	if projectRoot != "" {
		roots = append(roots,
			Root{filepath.Join(projectRoot, ".strument", "skills"), ScopeProject},
			Root{filepath.Join(projectRoot, ".agents", "skills"), ScopeProject},
		)
	}
	data, err := DataDir()
	if err != nil {
		return roots, err
	}
	roots = append(roots, Root{filepath.Join(data, "skills"), ScopeGlobal})
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, Root{filepath.Join(home, ".agents", "skills"), ScopeGlobal})
	}
	return roots, nil
}

// namePattern is the Agent Skills naming rule: lowercase letters, digits and
// hyphens, not starting or ending with one.
var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateName checks a skill name against the format's rules. The name is the
// handle a model uses to ask for a skill, so it has to be predictable.
func ValidateName(name string) error {
	switch {
	case name == "":
		return errors.New("name is required")
	case len(name) > 64:
		return errors.New("name is longer than 64 characters")
	case !namePattern.MatchString(name):
		return fmt.Errorf("name %q must be lowercase letters, digits and single hyphens, "+
			"not starting or ending with one", name)
	}
	return nil
}

// validateDescription checks the other required field.
func validateDescription(desc string) error {
	switch {
	case strings.TrimSpace(desc) == "":
		return errors.New("description is required")
	case len(desc) > 1024:
		return errors.New("description is longer than 1024 characters")
	}
	return nil
}

// Discover walks the roots and returns what it found, in precedence order,
// with a diagnostic for everything it refused.
//
// The first root to define a name wins; a later one is reported as shadowed
// rather than silently dropped, because a project skill quietly overriding a
// global one of the same name is worth knowing about.
func Discover(opts Options) ([]Skill, []Diagnostic) {
	roots := opts.Roots
	if roots == nil {
		// The error is dropped because DefaultRoots returns the roots it did
		// resolve alongside it: a session with no home directory still has its
		// project skills, and refusing all of them over a missing $HOME would
		// be the worse failure.
		roots, _ = DefaultRoots(opts.ProjectRoot)
	}

	var skills []Skill
	var diags []Diagnostic
	claimed := map[string]Root{}

	for _, root := range roots {
		if root.Path == "" {
			continue
		}
		found, rootDiags := scanRoot(root, opts.Trust)
		diags = append(diags, rootDiags...)
		for _, s := range found {
			if prior, taken := claimed[s.Name]; taken {
				diags = append(diags, Diagnostic{
					Path: s.Path,
					Message: fmt.Sprintf("shadowed by the %s skill of the same name in %s",
						prior.Scope, prior.Path),
				})
				continue
			}
			claimed[s.Name] = root
			skills = append(skills, s)
		}
	}
	return skills, diags
}

// scanRoot reads one directory of skills. A root that does not exist is not an
// error: most sessions have none of these directories.
func scanRoot(root Root, trust Truster) ([]Skill, []Diagnostic) {
	entries, err := os.ReadDir(root.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []Diagnostic{{Path: root.Path, Message: err.Error()}}
	}

	var skills []Skill
	var diags []Diagnostic
	for _, entry := range entries {
		if len(skills) >= MaxSkillsPerRoot {
			diags = append(diags, Diagnostic{
				Path:    root.Path,
				Message: fmt.Sprintf("more than %d skills; the rest were skipped", MaxSkillsPerRoot),
			})
			break
		}
		// A directory, or a symlink to one. os.ReadDir does not follow
		// symlinks, so a symlinked skill needs the explicit Stat below.
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		dir := filepath.Join(root.Path, entry.Name())
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			continue
		}
		// Three outcomes, not two: a skill, a refusal, or neither — a
		// directory that simply is not a skill, which a skills root has plenty
		// of. Treating "no diagnostic" as "a skill" dereferenced nil on a
		// README sitting beside the skills.
		s, diag := loadSkill(root, dir, entry.Name(), trust)
		switch {
		case diag != nil:
			diags = append(diags, *diag)
		case s != nil:
			skills = append(skills, *s)
		}
	}
	// Not sorted here: os.ReadDir already returns entries sorted by filename,
	// and a skill's name must equal its directory, so a root's skills come out
	// in name order for free. An explicit sort looked like it was buying
	// stability and was buying nothing — removing it broke no test, which is
	// how it was found.
	//
	// Across roots the order is precedence order, not name order, and that is
	// deliberate: the catalog should read in the order names are resolved.
	return skills, diags
}

// loadSkill reads and validates one skill directory.
func loadSkill(root Root, dir, dirName string, trust Truster) (*Skill, *Diagnostic) {
	path := filepath.Join(dir, "SKILL.md")
	refuse := func(format string, args ...any) (*Skill, *Diagnostic) {
		return nil, &Diagnostic{Path: path, Message: fmt.Sprintf(format, args...)}
	}

	info, err := os.Stat(path)
	if err != nil {
		// A directory without a SKILL.md is not a skill and not a complaint —
		// a skills root may hold a README or a shared references directory.
		return nil, nil
	}
	if !info.Mode().IsRegular() {
		return refuse("not a regular file")
	}
	if info.Size() > MaxSkillBytes {
		return refuse("larger than %d bytes", MaxSkillBytes)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return refuse("%v", err)
	}

	fm, body, err := Parse(string(src))
	if err != nil {
		return refuse("%v", err)
	}
	if err := ValidateName(fm.Name); err != nil {
		return refuse("%v", err)
	}
	if err := validateDescription(fm.Description); err != nil {
		return refuse("%v", err)
	}
	// The name is what a model asks for and the directory is what a human
	// sees. When they disagree the skill has two identities, and picking one
	// silently makes the other a lie.
	if fm.Name != dirName {
		return refuse("name %q does not match its directory %q", fm.Name, dirName)
	}

	// Trust is over the file's content, so editing a trusted skill revokes it.
	// Only SKILL.md is hashed, not the whole directory: a skill's scripts are
	// not special, and running one is a bash call that the permission system
	// already gates. Trusting the instructions is the whole decision, and
	// everything those instructions cause still goes through the usual gates.
	trusted := root.Scope == ScopeGlobal
	if !trusted && trust != nil {
		trusted = trust.IsTrusted(path, src)
	}
	return &Skill{
		Frontmatter: fm, Body: body,
		Path: path, Dir: dir, Root: root, Trusted: trusted,
	}, nil
}

// Usable returns only the skills whose instructions may reach the model.
//
// Discovery deliberately returns untrusted skills so the user can be told they
// exist; this is the filter that keeps them out of a prompt. Anything building
// a catalog or loading a body must go through it — an untrusted SKILL.md is
// text from whoever wrote the repository.
func Usable(skills []Skill) []Skill {
	out := make([]Skill, 0, len(skills))
	for _, s := range skills {
		if s.Trusted {
			out = append(out, s)
		}
	}
	return out
}

// Untrusted returns the skills that were found but may not be used, so a
// session can name them and say how to trust them.
func Untrusted(skills []Skill) []Skill {
	out := make([]Skill, 0)
	for _, s := range skills {
		if !s.Trusted {
			out = append(out, s)
		}
	}
	return out
}

// TrustablePaths lists the SKILL.md files under a project that `strument
// trust` should record, whether or not they are currently trusted. Re-running
// after an edit is what re-trusts an edited skill.
func TrustablePaths(projectRoot string) ([]string, []Diagnostic) {
	roots, _ := DefaultRoots(projectRoot)
	var project []Root
	for _, r := range roots {
		if r.Scope == ScopeProject {
			project = append(project, r)
		}
	}
	skills, diags := Discover(Options{ProjectRoot: projectRoot, Roots: project})
	paths := make([]string, 0, len(skills))
	for _, s := range skills {
		paths = append(paths, s.Path)
	}
	return paths, diags
}
