package checks_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/conform/internal/checks"
)

// renameRepo builds a git repo on main holding files, commits it, then
// branches and applies edits. The stale-name rule reads the branch's
// divergence from main, so a test only sees anything if both halves exist.
func renameRepo(t *testing.T, base, edits map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(files map[string]string) {
		t.Helper()
		for rel, body := range files {
			path := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}
	}

	git("init", "--initial-branch=main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	// The fleet's own commit-lint hook is installed globally via
	// core.hooksPath and would judge this fixture's commit messages.
	git("config", "core.hooksPath", filepath.Join(dir, ".git", "nohooks"))
	write(base)
	git("add", "-A")
	git("commit", "-m", "base")
	git("checkout", "-b", "work")
	write(edits)
	git("add", "-A")

	return dir
}

// A rename is exactly the case the compiler half-handles: Go is consistent
// afterwards and the prose is not.
func TestStaleNameFindsTheReferenceGoNoLongerHolds(t *testing.T) {
	dir := renameRepo(t,
		map[string]string{
			"main.go":  "package main\n\nfunc Reindex() {}\n",
			"docs.md":  "Run Reindex to rebuild.\n",
			"hook.sh":  "#!/bin/sh\nmnemd Reindex\n",
			"go.mod":   "module x\n\ngo 1.22\n",
			"unrel.md": "nothing here\n",
		},
		map[string]string{"main.go": "package main\n\nfunc Index() {}\n"},
	)

	findings := checks.CheckStaleNames(dir, nil)
	if len(findings) != 2 {
		t.Fatalf("want a finding for each surviving reference, got %d: %v", len(findings), findings)
	}

	files := []string{findings[0].File, findings[1].File}
	for _, want := range []string{"docs.md", "hook.sh"} {
		if !contains(files, want) {
			t.Errorf("want a finding naming %s, got %v", want, files)
		}
	}

	wantLine := map[string]string{"docs.md": "line 1", "hook.sh": "line 2"}

	for _, f := range findings {
		if f.Rule != checks.RuleStaleName {
			t.Errorf("rule = %q, want %q", f.Rule, checks.RuleStaleName)
		}
		if !strings.Contains(f.Msg, wantLine[f.File]) {
			t.Errorf("%s: msg must name %s, got %q", f.File, wantLine[f.File], f.Msg)
		}
		if !strings.Contains(f.Msg, "Reindex") {
			t.Errorf("%s: msg must name the dead word, got %q", f.File, f.Msg)
		}
		if f.Repair == "" {
			t.Error("every finding carries a repair")
		}
	}
}

// A CLI verb is renamed in a table, never in declaration position, so the
// quoted-literal half of the parser is what catches it. This is the mnemd
// reindex->index case that motivated the rule.
func TestStaleNameFindsARenamedCLIVerb(t *testing.T) {
	dir := renameRepo(t,
		map[string]string{
			"main.go": "package main\n\nvar verbs = []string{\"capture\", \"reindex\"}\n",
			"adr.md":  "# ADR\n\nThe sweeper is `mnemd reindex`.\n",
			"go.mod":  "module x\n\ngo 1.22\n",
		},
		map[string]string{"main.go": "package main\n\nvar verbs = []string{\"capture\", \"index\"}\n"},
	)

	findings := checks.CheckStaleNames(dir, nil)
	if len(findings) != 1 || findings[0].File != "adr.md" {
		t.Fatalf("want one finding in adr.md, got %v", findings)
	}
	if !strings.Contains(findings[0].Msg, "reindex") {
		t.Errorf("msg must name the dead word, got %q", findings[0].Msg)
	}
}

// Moving a declaration deletes the same line a rename does. What separates
// them is whether the name is still Go afterwards, so the rule must stay
// quiet here without knowing anything about moves.
func TestStaleNameIgnoresAMovedDeclaration(t *testing.T) {
	dir := renameRepo(t,
		map[string]string{
			"main.go": "package main\n\nfunc Reindex() {}\n",
			"docs.md": "Run Reindex to rebuild.\n",
			"go.mod":  "module x\n\ngo 1.22\n",
		},
		map[string]string{
			"main.go":  "package main\n",
			"moved.go": "package main\n\nfunc Reindex() {}\n",
		},
	)

	if findings := checks.CheckStaleNames(dir, nil); findings != nil {
		t.Fatalf("a moved declaration is not a rename, got %v", findings)
	}
}

// Dropping one of two declarations that share a name leaves the word live in
// Go; the docs that use it are correct, not stale.
func TestStaleNameIgnoresANameStillDeclaredElsewhere(t *testing.T) {
	dir := renameRepo(t,
		map[string]string{
			"a.go":    "package main\n\nfunc Reindex() {}\n",
			"b.go":    "package other\n\nfunc Reindex() {}\n",
			"docs.md": "Run Reindex to rebuild.\n",
			"go.mod":  "module x\n\ngo 1.22\n",
		},
		map[string]string{"a.go": "package main\n"},
	)

	if findings := checks.CheckStaleNames(dir, nil); findings != nil {
		t.Fatalf("the name is still Go, got %v", findings)
	}
}

// A repo whose prose must go on saying a dead word declares it once, with a
// reason; the sweep then skips that name and only that name.
func TestStaleNameHonorsTheKeepList(t *testing.T) {
	dir := renameRepo(t,
		map[string]string{
			"main.go": "package main\n\nfunc Reindex() {}\n",
			"docs.md": "Run Reindex to rebuild.\n",
			"go.mod":  "module x\n\ngo 1.22\n",
		},
		map[string]string{"main.go": "package main\n\nfunc Index() {}\n"},
	)

	if findings := checks.CheckStaleNames(dir, map[string]bool{"Reindex": true}); findings != nil {
		t.Fatalf("a kept name is not swept, got %v", findings)
	}
}

// The rule's claim to a place in every `make check` is that it costs nothing
// when nothing was renamed. That is a property of the parser, not of timing:
// no declaration on a removed line means no name to sweep, so the check
// returns before it can touch the filesystem.
func TestRemovedNamesYieldsNothingWithoutADeclaration(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/main.go",
		"+++ b/main.go",
		"-	total := Reindex(ctx)",
		"+	total := Reindex(ctx, opts)",
	}, "\n")

	decls, verbs := checks.RemovedNames(diff)
	if len(decls) != 0 || len(verbs) != 0 {
		t.Fatalf("a changed call site declares nothing, got %v / %v", decls, verbs)
	}
}

func TestRemovedNamesReadsEveryDeclarationPosition(t *testing.T) {
	diff := strings.Join([]string{
		"+++ b/main.go",
		"-func Reindex() {}",
		"-func (s *Store) Adopt() {}",
		"-type Sweeper struct{}",
		"-const BannerPrefix = \"x\"",
		"-	verbName string = \"oldverb\"",
		"-	{name: \"another\"},",
		"-	x := 1",
	}, "\n")

	decls, verbs := checks.RemovedNames(diff)
	for _, want := range []string{"Adopt", "BannerPrefix", "Reindex", "Sweeper", "verbName"} {
		if !contains(decls, want) {
			t.Errorf("want %q among removed declarations, got %v", want, decls)
		}
	}

	for _, want := range []string{"another", "oldverb"} {
		if !contains(verbs, want) {
			t.Errorf("want %q among removed verbs, got %v", want, verbs)
		}
	}

	if contains(decls, "x") {
		t.Errorf("a name under the length floor is noise, got %v", decls)
	}
}

// contains is the test's own copy; the package has an unexported one.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}
