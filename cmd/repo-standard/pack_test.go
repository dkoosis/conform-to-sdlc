package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dkoosis/repo-standard/internal/lintpack"
)

// TestPackSyncWritesTheRequiredPack: the repair lint-pack names exists and
// writes the required files.
func TestPackSyncWritesTheRequiredPack(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := run([]string{"pack", "sync"}); err != nil {
		t.Fatalf("pack sync: %v", err)
	}
	for _, name := range []string{"bugclasses.go", "modern.go"} {
		if _, err := os.Stat(filepath.Join(dir, lintpack.Dir, name)); err != nil {
			t.Errorf("pack sync did not write %s: %v", name, err)
		}
	}
	if err := run([]string{"pack", "bogus"}); !errors.Is(err, errUsage) {
		t.Errorf("pack bogus: got %v, want a usage error", err)
	}
}

// TestFixThatCannotWriteStillReports: --fix in a directory it cannot write
// writes nothing and still runs the check, so the findings are printed and the
// exit is the findings exit.
func TestFixThatCannotWriteStillReports(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // restore so TempDir can clean up
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		_ = f.Close() // probe only
		t.Skip("running as a user who can write a 0500 directory (root); unwritable case cannot be built")
	}
	t.Chdir(dir)

	if err := run([]string{"--fix"}); !errors.Is(err, errFindings) {
		t.Fatalf("--fix in an unwritable repo: got %v, want the findings exit", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, lintpack.Dir)); !os.IsNotExist(err) {
		t.Errorf("--fix left %s behind in an unwritable repo (err %v)", lintpack.Dir, err)
	}
}
