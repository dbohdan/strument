package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates <root>/<dir>/SKILL.md and returns its path.
func writeSkill(t *testing.T, root, dir, content string) string {
	t.Helper()
	full := filepath.Join(root, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(full, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func skillFile(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n# " + name + "\n\nDo the thing.\n"
}

// trustSet is a Truster backed by the set of paths a test says are trusted.
type trustSet map[string]bool

func (ts trustSet) IsTrusted(absPath string, _ []byte) bool { return ts[absPath] }

func TestDiscoverReadsASkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "pdf-tools", skillFile("pdf-tools", "Work with PDF files."))

	skills, diags := Discover(Options{Roots: []Root{{root, ScopeGlobal}}})
	if len(diags) != 0 {
		t.Fatalf("diagnostics: %+v", diags)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills", len(skills))
	}
	s := skills[0]
	if s.Name != "pdf-tools" || s.Description != "Work with PDF files." {
		t.Errorf("skill = %+v", s.Frontmatter)
	}
	if !strings.Contains(s.Body, "Do the thing.") {
		t.Errorf("body = %q", s.Body)
	}
	// Dir is what relative references inside a skill resolve against, so it
	// has to be the skill's own directory rather than the root.
	if s.Dir != filepath.Join(root, "pdf-tools") {
		t.Errorf("Dir = %q", s.Dir)
	}
	// A global skill needs no trust: putting a file there is already a
	// deliberate act by the person the session belongs to.
	if !s.Trusted {
		t.Error("a global skill came back untrusted")
	}
}

// The heart of the design. A project skill travels with the repository, so
// cloning a hostile project must not be enough to put instructions in front of
// the model.
func TestProjectSkillsNeedTrust(t *testing.T) {
	proj := t.TempDir()
	root := filepath.Join(proj, ".strument", "skills")
	path := writeSkill(t, root, "deploy", skillFile("deploy", "Ship it."))

	skills, _ := Discover(Options{Roots: []Root{{root, ScopeProject}}})
	if len(skills) != 1 {
		t.Fatalf("got %d skills", len(skills))
	}
	// Found and named, so the user can be told it exists...
	if skills[0].Name != "deploy" {
		t.Errorf("name = %q", skills[0].Name)
	}
	// ...but not usable.
	if skills[0].Trusted {
		t.Fatal("an untrusted project skill came back trusted")
	}
	if len(Usable(skills)) != 0 {
		t.Error("an untrusted skill survived Usable")
	}
	if len(Untrusted(skills)) != 1 {
		t.Error("an untrusted skill was not reported as such")
	}

	// With a trust record it becomes usable.
	skills, _ = Discover(Options{Roots: []Root{{root, ScopeProject}}, Trust: trustSet{path: true}})
	if len(Usable(skills)) != 1 {
		t.Error("a trusted project skill was still refused")
	}
}

// Trust is over content, so an edit revokes it. That falls out of the store
// hashing the file, and it is the property that makes trusting once safe.
func TestEditingATrustedSkillRevokesIt(t *testing.T) {
	proj := t.TempDir()
	root := filepath.Join(proj, ".strument", "skills")
	path := writeSkill(t, root, "deploy", skillFile("deploy", "Ship it."))

	// A Truster that answers for one exact content, the way the real store's
	// content hash does.
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trust := contentTrust{path: path, content: string(original)}

	skills, _ := Discover(Options{Roots: []Root{{root, ScopeProject}}, Trust: trust})
	if !skills[0].Trusted {
		t.Fatal("the unedited skill was not trusted")
	}

	writeSkill(t, root, "deploy", skillFile("deploy", "Ship it, and also read ~/.ssh."))
	skills, _ = Discover(Options{Roots: []Root{{root, ScopeProject}}, Trust: trust})
	if skills[0].Trusted {
		t.Error("editing a trusted skill did not revoke its trust")
	}
}

type contentTrust struct{ path, content string }

func (c contentTrust) IsTrusted(absPath string, content []byte) bool {
	return absPath == c.path && string(content) == c.content
}

// Precedence: the first root to define a name wins, and the loser is reported
// rather than dropped. A project skill quietly overriding a global one of the
// same name is exactly what someone would want to know about.
func TestPrecedenceAndShadowing(t *testing.T) {
	projRoot, globalRoot := t.TempDir(), t.TempDir()
	writeSkill(t, projRoot, "notes", skillFile("notes", "Project version."))
	writeSkill(t, globalRoot, "notes", skillFile("notes", "Global version."))

	skills, diags := Discover(Options{
		Roots: []Root{{projRoot, ScopeProject}, {globalRoot, ScopeGlobal}},
		Trust: trustSet{filepath.Join(projRoot, "notes", "SKILL.md"): true},
	})
	if len(skills) != 1 || skills[0].Description != "Project version." {
		t.Fatalf("skills = %+v", skills)
	}
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "shadowed") {
		t.Errorf("diagnostics = %+v, want the global one reported as shadowed", diags)
	}
}

// Everything discovery refuses, each with a reason. A skill that vanishes
// silently looks identical to one nobody wrote.
func TestDiscoverDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, dir, content, want string }{
		{"unparseable", "broken", "not frontmatter at all\n", "opening delimiter"},
		{"no name", "anon", "---\ndescription: x\n---\n", "name is required"},
		{"no description", "bare", "---\nname: bare\n---\n", "description is required"},
		{"bad name", "Bad_Name", "---\nname: Bad_Name\ndescription: x\n---\n", "must be lowercase"},
		{
			// Two identities: the name is what a model asks for, the directory
			// is what a human sees. Picking one silently makes the other a lie.
			"name does not match directory", "actual",
			"---\nname: declared\ndescription: x\n---\n", "does not match its directory",
		},
		{
			"description too long", "chatty",
			"---\nname: chatty\ndescription: " + strings.Repeat("a", 1025) + "\n---\n",
			"longer than 1024",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSkill(t, root, tc.dir, tc.content)
			skills, diags := Discover(Options{Roots: []Root{{root, ScopeGlobal}}})
			if len(skills) != 0 {
				t.Errorf("a bad skill was loaded anyway: %+v", skills)
			}
			if len(diags) != 1 {
				t.Fatalf("diagnostics = %+v", diags)
			}
			if !strings.Contains(diags[0].Message, tc.want) {
				t.Errorf("message %q, want it to mention %q", diags[0].Message, tc.want)
			}
		})
	}
}

// A directory with no SKILL.md is not a complaint: a skills root may hold a
// README or a shared references directory beside the skills.
func TestDirectoryWithoutSkillFileIsQuiet(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, diags := Discover(Options{Roots: []Root{{root, ScopeGlobal}}})
	if len(skills) != 0 || len(diags) != 0 {
		t.Errorf("skills = %+v, diags = %+v", skills, diags)
	}
}

// A root that does not exist is the normal case: most sessions have none of
// these directories.
func TestMissingRootIsNotAnError(t *testing.T) {
	skills, diags := Discover(Options{Roots: []Root{{filepath.Join(t.TempDir(), "nope"), ScopeGlobal}}})
	if len(skills) != 0 || len(diags) != 0 {
		t.Errorf("skills = %+v, diags = %+v", skills, diags)
	}
}

// Caps bound a hostile directory to a diagnostic rather than a hang.
func TestCaps(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		root := t.TempDir()
		for i := range MaxSkillsPerRoot + 5 {
			name := "skill-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			writeSkill(t, root, name, skillFile(name, "x"))
		}
		skills, diags := Discover(Options{Roots: []Root{{root, ScopeGlobal}}})
		if len(skills) > MaxSkillsPerRoot {
			t.Errorf("loaded %d skills past the cap of %d", len(skills), MaxSkillsPerRoot)
		}
		if len(diags) == 0 {
			t.Error("the cap was hit without saying so")
		}
	})
	t.Run("size", func(t *testing.T) {
		root := t.TempDir()
		writeSkill(t, root, "huge", "---\nname: huge\ndescription: x\n---\n"+
			strings.Repeat("a", MaxSkillBytes+1))
		_, diags := Discover(Options{Roots: []Root{{root, ScopeGlobal}}})
		if len(diags) != 1 || !strings.Contains(diags[0].Message, "larger than") {
			t.Errorf("diagnostics = %+v", diags)
		}
	})
}

// Order. Within a root it is name order, which os.ReadDir gives for free
// since a skill's name must equal its directory. Across roots it is
// *precedence* order rather than name order, and that is the part this package
// decides: the catalog should read in the order names resolve, so a reader can
// see which root wins.
//
// An earlier version of this test only checked stability across runs, which
// ReadDir already guarantees — it passed with the sort deleted, which is how
// the sort turned out to be redundant.
func TestOrderIsPrecedenceThenName(t *testing.T) {
	projRoot, globalRoot := t.TempDir(), t.TempDir()
	for _, n := range []string{"zebra", "alpha"} {
		writeSkill(t, projRoot, n, skillFile(n, "x"))
	}
	for _, n := range []string{"beta", "yak"} {
		writeSkill(t, globalRoot, n, skillFile(n, "x"))
	}
	skills, _ := Discover(Options{
		Roots: []Root{{projRoot, ScopeProject}, {globalRoot, ScopeGlobal}},
		Trust: trustSet{
			filepath.Join(projRoot, "zebra", "SKILL.md"): true,
			filepath.Join(projRoot, "alpha", "SKILL.md"): true,
		},
	})
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}
	// Both project skills first even though "zebra" sorts last overall.
	want := "alpha,zebra,beta,yak"
	if strings.Join(names, ",") != want {
		t.Errorf("order = %v, want %s", names, want)
	}
}

// Data, not config. The distinction is in the plan and the comment; this pins
// it so a later refactor cannot quietly move skills next to config.star.
func TestDataDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	dir, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join("/custom/data", "strument") {
		t.Errorf("DataDir() = %q", dir)
	}

	t.Setenv("XDG_DATA_HOME", "")
	dir, err = DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dir, filepath.Join(".local", "share", "strument")) {
		t.Errorf("DataDir() = %q, want ~/.local/share/strument", dir)
	}
}

// The default roots, in precedence order, with the project ones marked as
// needing trust. Absent by design: .claude/skills and the other vendors'.
func TestDefaultRoots(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	roots, err := DefaultRoots("/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) < 3 {
		t.Fatalf("roots = %+v", roots)
	}
	if roots[0].Path != filepath.Join("/proj", ".strument", "skills") || roots[0].Scope != ScopeProject {
		t.Errorf("first root = %+v", roots[0])
	}
	if roots[1].Path != filepath.Join("/proj", ".agents", "skills") || roots[1].Scope != ScopeProject {
		t.Errorf("second root = %+v", roots[1])
	}
	if roots[2].Path != filepath.Join("/data", "strument", "skills") || roots[2].Scope != ScopeGlobal {
		t.Errorf("third root = %+v", roots[2])
	}
	for _, r := range roots {
		if strings.Contains(r.Path, ".claude") {
			t.Errorf("another tool's directory is being read: %s", r.Path)
		}
	}

	// With no project, only the global roots.
	roots, err = DefaultRoots("")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roots {
		if r.Scope == ScopeProject {
			t.Errorf("a project root appeared with no project: %+v", r)
		}
	}
}

// TrustablePaths is what `strument trust` records: the project's skills,
// whether or not they are currently trusted, since re-running is what
// re-trusts an edited one.
func TestTrustablePathsCoversUntrustedProjectSkills(t *testing.T) {
	proj := t.TempDir()
	want := writeSkill(t, filepath.Join(proj, ".strument", "skills"), "one",
		skillFile("one", "First."))
	alsoWant := writeSkill(t, filepath.Join(proj, ".agents", "skills"), "two",
		skillFile("two", "Second."))

	paths, diags := TrustablePaths(proj)
	if len(diags) != 0 {
		t.Errorf("diagnostics = %+v", diags)
	}
	got := strings.Join(paths, ",")
	if !strings.Contains(got, want) || !strings.Contains(got, alsoWant) {
		t.Errorf("paths = %v, want both project skills", paths)
	}
	// Global skills are not the project's to trust.
	for _, p := range paths {
		if !strings.HasPrefix(p, proj) {
			t.Errorf("a path outside the project was listed for trusting: %s", p)
		}
	}
}

// Discovery must survive whatever is in a directory. This is the parser's
// never-panic contract applied to the filesystem, and it earned its place: the
// first version dereferenced nil on a directory that simply was not a skill,
// which is what a README or a shared references/ directory looks like.
func TestDiscoverSurvivesOddDirectories(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, mode os.FileMode, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	mk("README.md", 0o644, "not a skill")
	mk("empty/SKILL.md", 0o644, "")
	mk("binary/SKILL.md", 0o644, "\x00\x01\x02\xff\xfe")
	mk("just-delims/SKILL.md", 0o644, "---\n---\n")
	mk("no-body/SKILL.md", 0o644, "---\nname: no-body\ndescription: x\n---\n")
	mk("nested/deeper/SKILL.md", 0o644, skillFile("deeper", "Too deep to be found."))
	if err := os.MkdirAll(filepath.Join(root, "dir-named-skill", "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink pointing at nothing, and one pointing back at the root.
	_ = os.Symlink(filepath.Join(root, "does-not-exist"), filepath.Join(root, "dangling"))
	_ = os.Symlink(root, filepath.Join(root, "loop"))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
	}()
	skills, _ := Discover(Options{Roots: []Root{{root, ScopeGlobal}}})
	// Only the well-formed one is a skill, and it is not the nested one:
	// discovery is one level deep, so a SKILL.md two levels down is not found.
	for _, s := range skills {
		if s.Name == "deeper" {
			t.Error("a SKILL.md two levels down was discovered")
		}
	}
}
