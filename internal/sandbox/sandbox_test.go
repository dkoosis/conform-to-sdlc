package sandbox_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/sandbox"
)

// requiredFiles are the library members every fleet repo must carry. lib-env.sh
// is the newest and the reason the package exists: setup and activation read
// their Go environment from it, so a build that drops it reintroduces the
// two-env-blocks bug.
var requiredFiles = []string{
	"Makefile.cross.mk",
	"Makefile.doctor.mk",
	"lib-activate.sh",
	"lib-doctor.sh",
	"lib-env.sh",
	"lib-setup.sh",
}

func TestFilesCarriesTheWholeLibrary(t *testing.T) {
	files, err := sandbox.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	for _, name := range requiredFiles {
		body, ok := files[name]
		if !ok {
			t.Errorf("canonical library is missing %s", name)
			continue
		}
		if len(body) == 0 {
			t.Errorf("%s is embedded but empty", name)
		}
	}
}

// The failure this whole package exists to prevent: setup warming one cache
// and activation reading another. Both must reach the environment through
// lib-env.sh, and lib-env.sh must not pin the toolchain to the base image.
func TestBothConsumersSourceTheOneEnvFile(t *testing.T) {
	files, err := sandbox.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	for _, name := range []string{"lib-activate.sh", "lib-setup.sh"} {
		if !strings.Contains(string(files[name]), "lib-env.sh") {
			t.Errorf("%s does not source lib-env.sh — setup and activation can disagree again", name)
		}
	}
	env := string(files["lib-env.sh"])
	if !strings.Contains(env, "GOTOOLCHAIN=auto") {
		t.Error("lib-env.sh must set GOTOOLCHAIN=auto; local fails whenever go.mod outruns the base image")
	}
	if strings.Contains(env, `export GOMODCACHE=`) || strings.Contains(env, `export GOCACHE=`) {
		t.Error("lib-env.sh must leave the Go caches ambient, not repoint them per repo")
	}
}

// The surmado review of dkoosis/ferret#174 and dkoosis/mnemd#167: the bat
// download had no pinned checksum, unlike the hyperfine case beside it.
func TestBatDownloadIsCheckedAndBounded(t *testing.T) {
	files, err := sandbox.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	mk := string(files["Makefile.cross.mk"])

	if !strings.Contains(mk, "BAT_SHA256_AMD64") || !strings.Contains(mk, "BAT_SHA256_ARM64") {
		t.Error("Makefile.cross.mk does not pin a BAT_SHA256 per architecture")
	}

	batIdx := strings.Index(mk, "bat)")
	if batIdx < 0 {
		t.Fatal("Makefile.cross.mk has no bat case block")
	}
	hfIdx := strings.Index(mk, "hyperfine)")
	if hfIdx < 0 {
		t.Fatal("Makefile.cross.mk has no hyperfine case block")
	}
	batBlock := mk[batIdx:hfIdx]

	if !strings.Contains(batBlock, "sha256 mismatch") {
		t.Error("Makefile.cross.mk does not fail the bat download on a checksum mismatch")
	}
	if !strings.Contains(batBlock, "--connect-timeout") || !strings.Contains(batBlock, "--max-time") {
		t.Error("bat's curl call is missing --connect-timeout/--max-time")
	}
}

// The surmado review of dkoosis/ferret#173: the hyperfine download had no
// pinned checksum, no curl timeout, and mismatched libc (musl on amd64, glibc
// on arm64) with no explanation.
func TestHyperfineDownloadIsCheckedAndBounded(t *testing.T) {
	files, err := sandbox.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	mk := string(files["Makefile.cross.mk"])

	if !strings.Contains(mk, "HYPERFINE_SHA256_AMD64") || !strings.Contains(mk, "HYPERFINE_SHA256_ARM64") {
		t.Error("Makefile.cross.mk does not pin a HYPERFINE_SHA256 per architecture")
	}
	if !strings.Contains(mk, "sha256 mismatch") {
		t.Error("Makefile.cross.mk does not fail the hyperfine download on a checksum mismatch")
	}

	hfIdx := strings.Index(mk, "hyperfine)")
	if hfIdx < 0 {
		t.Fatal("Makefile.cross.mk has no hyperfine case block")
	}
	hfBlock := mk[hfIdx:]
	if !strings.Contains(hfBlock, "--connect-timeout") || !strings.Contains(hfBlock, "--max-time") {
		t.Error("hyperfine's curl call is missing --connect-timeout/--max-time")
	}

	// amd64 stays musl; arm64 must either match musl too, or the file must say
	// why not (hyperfine ships no aarch64-unknown-linux-musl release).
	if strings.Contains(hfBlock, `arm64) HF_TRIPLE="aarch64-unknown-linux-gnu"`) &&
		!strings.Contains(mk, "aarch64-unknown-linux-musl") {
		t.Error("hyperfine arm64 is not musl, and the file does not say why not")
	}
}

// Sonar shell:S6505 on fo's synced lib-setup.sh (cfm-qdj): a global npm
// install that runs lifecycle scripts lets a compromised jscpd dependency run
// code at setup time.
func TestJscpdInstallSkipsLifecycleScripts(t *testing.T) {
	files, err := sandbox.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	for line := range strings.Lines(string(files["lib-setup.sh"])) {
		if strings.Contains(line, "npm install -g jscpd") && !strings.Contains(line, "--ignore-scripts") {
			t.Errorf("lib-setup.sh installs jscpd without --ignore-scripts: %s", strings.TrimSpace(line))
		}
	}
}

func TestSyncWritesThenReportsNoChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, sandbox.LibDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	changed, err := sandbox.Sync(dir)
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if len(changed) != len(requiredFiles) {
		t.Errorf("first Sync changed %d files, want %d", len(changed), len(requiredFiles))
	}

	changed, err = sandbox.Sync(dir)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("second Sync changed %v, want nothing — Sync is not idempotent", changed)
	}
}

func TestSyncOverwritesDriftAndSparesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := sandbox.Sync(dir); err != nil {
		t.Fatalf("seed Sync: %v", err)
	}

	drifted := filepath.Join(dir, sandbox.LibDir, "lib-env.sh")
	if err := os.WriteFile(drifted, []byte("export GOTOOLCHAIN=local\n"), 0o644); err != nil {
		t.Fatalf("write drift: %v", err)
	}
	foreign := filepath.Join(dir, sandbox.LibDir, "lib-repo-extra.sh")
	if err := os.WriteFile(foreign, []byte("# mine\n"), 0o644); err != nil {
		t.Fatalf("write foreign: %v", err)
	}

	changed, err := sandbox.Sync(dir)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(changed) != 1 || changed[0] != "lib-env.sh" {
		t.Errorf("Sync changed %v, want only lib-env.sh", changed)
	}
	if body, err := os.ReadFile(foreign); err != nil || string(body) != "# mine\n" {
		t.Errorf("Sync clobbered a file conform-to-sdlc does not own: %q, %v", body, err)
	}
}
