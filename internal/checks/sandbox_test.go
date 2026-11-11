package checks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/checks"
	"github.com/dkoosis/repo-standard/internal/sandbox"
)

func sandboxFindings(t *testing.T, dir string) []checks.Finding {
	t.Helper()
	var out []checks.Finding
	for _, f := range checks.Run(dir) {
		if f.Rule == checks.RuleSandboxLib {
			out = append(out, f)
		}
	}
	return out
}

// A repo with no .sandbox/ has opted out; the rule must not opt it back in.
func TestSandboxLibSilentWithoutASandbox(t *testing.T) {
	if f := sandboxFindings(t, t.TempDir()); len(f) != 0 {
		t.Errorf("got %v, want no findings for a repo with no sandbox", f)
	}
}

func TestSandboxLibCleanAfterSync(t *testing.T) {
	dir := t.TempDir()
	if _, err := sandbox.Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if f := sandboxFindings(t, dir); len(f) != 0 {
		t.Errorf("got %v, want no findings right after a sync", f)
	}
}

// The drift this rule exists for: nine repos stamped GO_SANDBOX_REF=v0.2.0 while
// carrying four different lib-activate.sh files.
func TestSandboxLibReportsDriftPerFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := sandbox.Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	path := filepath.Join(dir, sandbox.LibDir, "lib-activate.sh")
	if err := os.WriteFile(path, []byte("# hand-edited\n"), 0o644); err != nil {
		t.Fatalf("write drift: %v", err)
	}

	found := sandboxFindings(t, dir)
	if len(found) != 1 {
		t.Fatalf("got %d findings, want exactly the one drifted file: %v", len(found), found)
	}
	if !strings.HasSuffix(found[0].File, "lib-activate.sh") {
		t.Errorf("finding names %s, want the drifted file", found[0].File)
	}
	if !strings.Contains(found[0].Repair, "sandbox sync") {
		t.Errorf("repair is %q, want the command that fixes it", found[0].Repair)
	}
}

func TestSandboxLibReportsAMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := sandbox.Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, sandbox.LibDir, "lib-env.sh")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	found := sandboxFindings(t, dir)
	if len(found) != 1 || !strings.Contains(found[0].Msg, "missing") {
		t.Fatalf("got %v, want one missing-file finding", found)
	}
}
