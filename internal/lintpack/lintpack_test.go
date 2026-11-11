package lintpack_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/lintpack"
)

// gitBlobID is the id git gives a file's content: SHA-1 over a header and the
// bytes. It lets the test tie the embed to a lintbrush commit without one.
func gitBlobID(body []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(body))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// TestEmbedMatchesRecordedCommit: every embedded pack file is the blob SOURCE
// records for the lintbrush commit. A hand edit to _gorules/ without make
// pack-refresh fails here; `git -C <lintbrush> rev-parse <commit>:gorules/<f>`
// prints the same ids.
func TestEmbedMatchesRecordedCommit(t *testing.T) {
	t.Parallel()

	src, err := lintpack.ReadSource()
	if err != nil {
		t.Fatalf("ReadSource: %v", err)
	}
	if len(src.Commit) != 40 {
		t.Errorf("SOURCE commit %q is not a full git sha", src.Commit)
	}
	recorded := make([]string, 0, len(src.Blobs))
	for name := range src.Blobs {
		recorded = append(recorded, name)
	}
	var members []string
	for _, m := range lintpack.Members {
		members = append(members, m.Source)
		body, err := lintpack.Bytes(m.Source)
		if err != nil {
			t.Errorf("%s is a pack member but not embedded: %v", m.Source, err)
			continue
		}
		if got, want := gitBlobID(body), src.Blobs[m.Source]; got != want {
			t.Errorf("%s: embedded blob %s, SOURCE records %q for lintbrush %s; run make pack-refresh", m.Source, got, want, src.Commit)
		}
	}
	slices.Sort(recorded)
	slices.Sort(members)
	if !slices.Equal(recorded, members) {
		t.Errorf("SOURCE lists %v, the pack is %v", recorded, members)
	}
}

// TestSyncKeepsOptInOptIn: sync writes the required files, writes the opt-in
// file only where the repo already holds it, and rewrites drift in both.
func TestSyncKeepsOptInOptIn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	changed, err := lintpack.Sync(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bugclasses.go", "modern.go"}; !slices.Equal(changed, want) {
		t.Errorf("Sync on an empty repo wrote %v, want %v", changed, want)
	}
	optin := filepath.Join(dir, lintpack.Dir, "optin_rawstatewrite.go")
	if _, err := os.Stat(optin); err == nil {
		t.Fatal("Sync wrote the opt-in rule file into a repo that had not opted in")
	}

	if err := os.WriteFile(optin, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err = lintpack.Sync(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"optin_rawstatewrite.go"}; !slices.Equal(changed, want) {
		t.Errorf("Sync over a stale opt-in file changed %v, want %v", changed, want)
	}
	want, err := lintpack.Bytes("optin_rawstatewrite.go")
	if err != nil {
		t.Fatal(err)
	}
	if have, err := os.ReadFile(optin); err != nil || !bytes.Equal(have, want) {
		t.Errorf("opt-in file not rewritten to the embed (err %v)", err)
	}
}

// TestNoNetworkInThePackPath: the lint-pack code path imports nothing that can
// reach a network or run gh, so the check cannot go quiet on a dead upstream
// the way check-pack-drift.sh did.
func TestNoNetworkInThePackPath(t *testing.T) {
	t.Parallel()

	banned := []string{"net", "net/http", "os/exec"}
	for _, file := range []string{"lintpack.go", "../checks/lintpack.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(banned, p) {
				t.Errorf("%s imports %s; the pack check must stay offline", file, p)
			}
		}
	}
}
