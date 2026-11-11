package checks_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/checks"
	"github.com/dkoosis/repo-standard/internal/lintpack"
)

func packBytes(t *testing.T, source string) string {
	t.Helper()
	b, err := lintpack.Bytes(source)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// oneByteOff returns s with its last byte changed.
func oneByteOff(s string) string {
	last := s[len(s)-1]
	return s[:len(s)-1] + string(last^1)
}

// TestLintPack: each repo pack state against the embed, one fixture per row.
func TestLintPack(t *testing.T) {
	t.Parallel()

	const optin = lintpack.Dir + "/optin_rawstatewrite.go"
	tests := []struct {
		name     string
		edit     func(t *testing.T, files map[string]string)
		wantFile string // "" = the repo passes lint-pack
		wantMsg  string
	}{
		{
			name: "whole pack",
			edit: func(*testing.T, map[string]string) {},
		},
		{
			name: "bugclasses.go one byte off",
			edit: func(_ *testing.T, f map[string]string) {
				f[lintpack.Dir+"/bugclasses.go"] = oneByteOff(f[lintpack.Dir+"/bugclasses.go"])
			},
			wantFile: lintpack.Dir + "/bugclasses.go",
			wantMsg:  "differs from lintbrush gorules/rules.go",
		},
		{
			name:     "modern.go missing",
			edit:     func(_ *testing.T, f map[string]string) { delete(f, lintpack.Dir+"/modern.go") },
			wantFile: lintpack.Dir + "/modern.go",
			wantMsg:  "missing",
		},
		{
			name: "opt-in file byte-equal",
			edit: func(t *testing.T, f map[string]string) { t.Helper(); f[optin] = packBytes(t, "optin_rawstatewrite.go") },
		},
		{
			name: "opt-in file one byte off",
			edit: func(t *testing.T, f map[string]string) {
				t.Helper()
				f[optin] = oneByteOff(packBytes(t, "optin_rawstatewrite.go"))
			},
			wantFile: optin,
			wantMsg:  "differs",
		},
		{
			name: "no pack directory",
			edit: func(_ *testing.T, f map[string]string) {
				for k := range f {
					if strings.HasPrefix(k, lintpack.Dir+"/") {
						delete(f, k)
					}
				}
			},
			wantFile: lintpack.Dir + "/",
			wantMsg:  "no lint rules pack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := goodRepo()
			tt.edit(t, files)
			dir := writeRepo(t, files)

			// Through Run, so the rule is registered and nothing else fires.
			findings := checks.Run(dir)
			if tt.wantFile == "" {
				if len(findings) != 0 {
					t.Fatalf("want no findings, got %v", findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("want one lint-pack finding, got %v", findings)
			}
			f := findings[0]
			if f.Rule != checks.RuleLintPack || f.File != tt.wantFile || !strings.Contains(f.Msg, tt.wantMsg) {
				t.Errorf("got %s %s %q, want lint-pack %s containing %q", f.Rule, f.File, f.Msg, tt.wantFile, tt.wantMsg)
			}
			if !strings.Contains(f.Repair, "repo-standard") {
				t.Errorf("repair %q names no repo-standard command", f.Repair)
			}
		})
	}
}

// TestLintPack_RepairCommandForDrift: a drifted file's repair is pack sync,
// the command that rewrites it.
func TestLintPack_RepairCommandForDrift(t *testing.T) {
	t.Parallel()

	files := goodRepo()
	files[lintpack.Dir+"/bugclasses.go"] = oneByteOff(files[lintpack.Dir+"/bugclasses.go"])
	findings := checks.CheckLintPack(writeRepo(t, files))
	if len(findings) != 1 || !strings.Contains(findings[0].Repair, "go tool repo-standard pack sync") {
		t.Fatalf("want one finding repaired by pack sync, got %v", findings)
	}
}

// TestLintPack_MakefileCallsPackDrift: a make target that runs the retired
// script fails makefile-verbs; a comment naming it does not.
func TestLintPack_MakefileCallsPackDrift(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		extra string
		want  int
	}{
		{"target calls script", "pack-drift: ## Drift check\n\t@.golangci-rules/check-pack-drift.sh\n", 1},
		{"comment only", "# check-pack-drift.sh is retired; lint-pack covers the pack\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			files := goodRepo()
			files["Makefile"] = goodMakefile + tc.extra
			got := rulesOf(checks.CheckLintPack(writeRepo(t, files)))[checks.RuleMakefileVerb]
			if got != tc.want {
				t.Errorf("makefile-verbs findings = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRetiredFiles_PackDriftUnderRulesDir: the copy repos kept beside the
// pack is retired like the scripts/ one.
func TestRetiredFiles_PackDriftUnderRulesDir(t *testing.T) {
	t.Parallel()

	files := goodRepo()
	files[lintpack.Dir+"/check-pack-drift.sh"] = "#!/bin/sh\nexit 0\n"
	findings := checks.Run(writeRepo(t, files))
	if len(findings) != 1 || findings[0].Rule != checks.RuleRetiredFiles || findings[0].File != lintpack.Dir+"/check-pack-drift.sh" {
		t.Fatalf("want one retired-files finding for the pack's drift script, got %v", findings)
	}
}

// TestFix_LintPack: --fix writes the pack only where .golangci-rules/ is
// absent. A present file that differs, or a directory it cannot write, gets
// nothing written and lint-pack still names the problem.
func TestFix_LintPack(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		setup       func(t *testing.T, dir string)
		wantWrote   bool
		wantErr     bool
		wantFinding bool // lint-pack finding after the fix
	}{
		{
			name:      "absent",
			setup:     func(*testing.T, string) {},
			wantWrote: true,
		},
		{
			name: "present and differs",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				p := filepath.Join(dir, lintpack.Dir)
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(p, "bugclasses.go"), []byte("package local\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantFinding: true,
		},
		{
			name: "unwritable",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // restore so TempDir can clean up
				probe := filepath.Join(dir, "probe")
				if err := os.WriteFile(probe, nil, 0o600); err == nil {
					_ = os.Remove(probe) // probe only
					t.Skip("running as a user who can write a 0500 directory (root); unwritable case cannot be built")
				}
			},
			wantErr:     true,
			wantFinding: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			files := goodRepo()
			for k := range files {
				if strings.HasPrefix(k, lintpack.Dir+"/") {
					delete(files, k)
				}
			}
			dir := writeRepo(t, files)
			tc.setup(t, dir)
			before := snapshot(t, dir)

			done, err := checks.Fix(dir)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Fix error = %v, want error %v", err, tc.wantErr)
			}
			wrote := anyContains(done, lintpack.Dir)
			if wrote != tc.wantWrote {
				t.Errorf("Fix reported writing the pack = %v, want %v (%v)", wrote, tc.wantWrote, done)
			}
			if !tc.wantWrote {
				if after := snapshot(t, dir); after != before {
					t.Errorf("Fix changed the repo:\nbefore %s\nafter  %s", before, after)
				}
			}
			got := rulesOf(checks.Run(dir))[checks.RuleLintPack] > 0
			if got != tc.wantFinding {
				t.Errorf("lint-pack finding after Fix = %v, want %v", got, tc.wantFinding)
			}
		})
	}
}

// snapshot lists every path and size under dir, so a test can see that nothing
// was written.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b.WriteString(rel + ":" + strconv.FormatInt(info.Size(), 10) + " ")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestLintPack_Atomicfile: a repo whose go.mod requires dkoosis/atomicfile must
// hold the raw-state-write rule file byte-equal and name it in its ruleguard
// rules glob; a repo without atomicfile, and atomicfile itself, are left alone.
func TestLintPack_Atomicfile(t *testing.T) {
	t.Parallel()

	const (
		optin   = lintpack.Dir + "/optin_rawstatewrite.go"
		rulesIn = ",${base-path}/.golangci-rules/modern.go"
		direct  = "module example.com/app\n\ngo 1.26\n\nrequire github.com/dkoosis/atomicfile v0.3.1\n"
	)
	withOptin := func(t *testing.T, f map[string]string) {
		t.Helper()
		f[optin] = packBytes(t, "optin_rawstatewrite.go")
	}
	withGlob := func(f map[string]string) {
		f[".golangci.yml"] = strings.Replace(f[".golangci.yml"], rulesIn, rulesIn+",${base-path}/"+optin, 1)
	}
	tests := []struct {
		name    string
		gomod   string
		edit    func(t *testing.T, f map[string]string)
		want    []string // substrings, one finding each; nil = passes
		wantRep string
	}{
		{
			name:  "requires atomicfile, file and glob in place",
			gomod: direct,
			edit:  func(t *testing.T, f map[string]string) { t.Helper(); withOptin(t, f); withGlob(f) },
		},
		{
			name:  "requires atomicfile in a block",
			gomod: "module example.com/app\n\nrequire (\n\tgithub.com/dkoosis/atomicfile v0.3.1\n\tgopkg.in/yaml.v3 v3.0.1\n)\n",
			edit:  func(t *testing.T, f map[string]string) { t.Helper(); withOptin(t, f); withGlob(f) },
		},
		{
			name:    "requires atomicfile, file missing and glob missing",
			gomod:   direct,
			edit:    func(*testing.T, map[string]string) {},
			want:    []string{optin, "ruleguard.rules does not name"},
			wantRep: "ruleguard",
		},
		{
			name:    "requires atomicfile, file present but not in the glob",
			gomod:   direct,
			edit:    withOptin,
			want:    []string{"ruleguard.rules does not name " + optin},
			wantRep: "append",
		},
		{
			name:  "requires atomicfile, file one byte off",
			gomod: direct,
			edit: func(t *testing.T, f map[string]string) {
				t.Helper()
				f[optin] = oneByteOff(packBytes(t, "optin_rawstatewrite.go"))
				withGlob(f)
			},
			want:    []string{"differs from lintbrush gorules/optin_rawstatewrite.go"},
			wantRep: "pack sync",
		},
		{
			name:  "no atomicfile: silent without the file",
			gomod: "module example.com/app\n\ngo 1.26\n",
			edit:  func(*testing.T, map[string]string) {},
		},
		{
			name:  "no go.mod: silent",
			gomod: "",
			edit:  func(*testing.T, map[string]string) {},
		},
		{
			name:  "atomicfile only as an indirect requirement: silent",
			gomod: "module example.com/app\n\nrequire github.com/dkoosis/atomicfile v0.3.1 // indirect\n",
			edit:  func(*testing.T, map[string]string) {},
		},
		{
			name:  "the atomicfile repo itself is exempt",
			gomod: "module github.com/dkoosis/atomicfile\n\ngo 1.26\n\nrequire github.com/dkoosis/atomicfile v0.0.0 // self reference in a fixture\n",
			edit:  func(*testing.T, map[string]string) {},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files := goodRepo()
			if tt.gomod != "" {
				files["go.mod"] = tt.gomod
			}
			tt.edit(t, files)
			got := checks.CheckLintPack(writeRepo(t, files))
			if len(got) != len(tt.want) {
				t.Fatalf("lint-pack findings = %+v, want %d matching %q", got, len(tt.want), tt.want)
			}
			for _, w := range tt.want {
				if !slices.ContainsFunc(got, func(f checks.Finding) bool {
					return f.Rule == checks.RuleLintPack && strings.Contains(f.File+" "+f.Msg, w) && strings.Contains(f.Repair, tt.wantRep)
				}) {
					t.Errorf("no lint-pack finding with a repair naming %q matches %q in %+v", tt.wantRep, w, got)
				}
			}
		})
	}
}

// TestSync_Atomicfile: pack sync writes the raw-state-write file into a repo
// that requires atomicfile, and leaves it out of one that does not.
func TestSync_Atomicfile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, gomod string
		want        bool
	}{
		{"requires atomicfile", "module example.com/app\n\nrequire github.com/dkoosis/atomicfile v0.3.1\n", true},
		{"does not", "module example.com/app\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tc.gomod), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := lintpack.Sync(dir); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Join(dir, lintpack.Dir, "optin_rawstatewrite.go"))
			if got := err == nil; got != tc.want {
				t.Errorf("optin_rawstatewrite.go written = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFix_AtomicfileRepoPassesLintPack: --fix on a repo that requires atomicfile
// and has no pack or config writes the raw-state-write file and a config whose
// ruleguard rules name it, so lint-pack passes afterwards.
func TestFix_AtomicfileRepoPassesLintPack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\nrequire github.com/dkoosis/atomicfile v0.3.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	done, err := checks.Fix(dir)
	if err != nil {
		t.Fatal(err)
	}
	if msg := strings.Join(done, "\n"); !strings.Contains(msg, "rules: \"${base-path}/.golangci-rules/bugclasses.go,${base-path}/.golangci-rules/modern.go,${base-path}/.golangci-rules/optin_rawstatewrite.go\"") {
		t.Errorf("--fix's pack message omits the raw-write file: %s", msg)
	}
	if _, err := os.Stat(filepath.Join(dir, lintpack.Dir, "optin_rawstatewrite.go")); err != nil {
		t.Errorf("--fix did not write optin_rawstatewrite.go: %v", err)
	}
	if got := checks.CheckLintPack(dir); len(got) != 0 {
		t.Errorf("lint-pack after --fix = %+v, want none", got)
	}
}
