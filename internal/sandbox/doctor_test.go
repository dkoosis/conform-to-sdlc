package sandbox_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/sandbox"
)

// runDoctor writes the canonical library to a temp SANDBOX_DIR, installs the
// given fake prebuilts and installed tools, runs script under bash, and
// returns its combined output. Each fake is a shell script whose body decides
// which version probes it answers.
func runDoctor(t *testing.T, prebuilt, installed map[string]string, script string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	root := t.TempDir()
	sbx := filepath.Join(root, ".sandbox")
	pre := filepath.Join(root, "pre")
	inst := filepath.Join(root, "inst")
	for _, d := range []string{sbx, pre, inst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sandbox.Sync(root); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	conf := "BASE_IMAGE_TOOLS=\"\"\nPREBUILT_TOOLS=\"\"\nOPTIONAL_TOOLS=\"\"\n"
	if err := os.WriteFile(filepath.Join(sbx, "project.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(dir string, tools map[string]string) {
		for name, body := range tools {
			p := filepath.Join(dir, name)
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(pre, prebuilt)
	write(inst, installed)

	cmd := exec.Command(bash, "-c", "set -u; source \"$SANDBOX_DIR/lib/lib-doctor.sh\"; "+script)
	cmd.Env = []string{
		"PATH=" + inst + ":" + os.Getenv("PATH"),
		"SANDBOX_DIR=" + sbx,
		"REPO_DIR=" + root,
		"PREBUILT_DIR=" + pre,
		"INSTALL_DIR=" + inst,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	return string(out)
}

// nilaway is a go/analysis singlechecker: it rejects --version and reads
// `version` as a package pattern, and only answers -V=full.
func TestRestoreKeepsAToolThatOnlyAnswersDashVFull(t *testing.T) {
	out := runDoctor(t,
		map[string]string{"checker": `case "$1" in -V=full) echo v; exit 0;; esac; exit 2`},
		nil,
		`restore_sandbox_binaries; [ -x "$INSTALL_DIR/checker" ] && echo "kept ${REPAIRED_SUCCESS[*]}"`)
	if !strings.Contains(out, "kept true") {
		t.Errorf("a -V=full tool was not installed and reported repaired:\n%s", out)
	}
}

func TestRestoreRemovesAToolThatAnswersNoProbe(t *testing.T) {
	out := runDoctor(t,
		map[string]string{"broken": `exit 2`},
		nil,
		`restore_sandbox_binaries; [ -e "$INSTALL_DIR/broken" ] && echo present || echo "gone ${REPAIRED_SUCCESS[*]}"`)
	if !strings.Contains(out, "gone false") {
		t.Errorf("a no-probe tool was not removed with a failed repair:\n%s", out)
	}
}

// A container cached at an older commit keeps that commit's tools. The refresh
// overwrites the ones that differ from the committed prebuilt, by copy and with
// no version probe: dtree is a shell script and answers none, and a tool that
// fails a probe must not be deleted for it.
func TestRefreshStaleOverwritesADifferingToolWithoutProbing(t *testing.T) {
	answers := func(v string) string { return `case "$1" in --version) echo ` + v + `; exit 0;; esac; exit 2` }
	out := runDoctor(t,
		map[string]string{"old": answers("new"), "noprobe": "exit 2", "same": answers("same")},
		map[string]string{"old": answers("old"), "noprobe": "echo stale; exit 2", "same": answers("same")},
		`touch -t 202001010000 "$INSTALL_DIR/same"
		refresh_stale_sandbox_binaries >/dev/null
		cmp -s "$PREBUILT_DIR/old" "$INSTALL_DIR/old" && [ -x "$INSTALL_DIR/old" ] && echo "old refreshed"
		cmp -s "$PREBUILT_DIR/noprobe" "$INSTALL_DIR/noprobe" && [ -x "$INSTALL_DIR/noprobe" ] && echo "noprobe refreshed"
		[ -n "$(find "$INSTALL_DIR/same" -mtime +1)" ] && echo "same untouched"`)
	for _, want := range []string{"old refreshed", "noprobe refreshed", "same untouched"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestLibraryCarriesNoDropStaleFunction(t *testing.T) {
	files, err := sandbox.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if strings.Contains(string(files["lib-doctor.sh"]), "drop_stale_sandbox_binaries") {
		t.Error("lib-doctor.sh still defines drop_stale_sandbox_binaries; it is refresh_stale_sandbox_binaries")
	}
}
