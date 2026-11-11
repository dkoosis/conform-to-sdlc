// Package sandbox carries the canonical .sandbox/lib — the shared shell that
// every fleet repo's sandbox activation and container setup run.
//
// It lives here because conform-to-sdlc is already in every repo's go.mod as a tool
// and already runs on every `make check`. Before this, the same library was
// hand-copied into nine repos, each stamping the same version header:
// GO_SANDBOX_REF=v0.2.0 named four different lib-activate.sh files. A vendored
// copy with no checker drifts silently; a vendored copy conform-to-sdlc compares byte
// for byte cannot.
//
// The pinned conform-to-sdlc version in a repo's go.mod IS the sandbox-lib version.
// There is no second ref to keep in step.
package sandbox

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

//go:embed assets/lib
var assets embed.FS

// LibDir is the repo-relative directory the canonical files are written to.
const LibDir = ".sandbox/lib"

// assetDir is the embed path; embed.FS always uses forward slashes.
const assetDir = "assets/lib"

// Files returns the canonical library keyed by base name, with the bytes each
// file must contain. Callers must not mutate the returned slices.
func Files() (map[string][]byte, error) {
	entries, err := fs.ReadDir(assets, assetDir)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", assetDir, err)
	}
	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := assets.ReadFile(path.Join(assetDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", e.Name(), err)
		}
		out[e.Name()] = b
	}
	return out, nil
}

// Names returns the canonical file names, sorted, so output is deterministic.
func Names() ([]string, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// Sync writes every canonical file into dir/.sandbox/lib, overwriting drift.
// It reports the names it changed, in sorted order; an already-conforming repo
// yields none. Files under .sandbox/lib that conform-to-sdlc does not own are left
// alone — a repo may keep its own helpers there.
func Sync(dir string) ([]string, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	target := filepath.Join(dir, LibDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", LibDir, err)
	}
	names, err := Names()
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, name := range names {
		dest := filepath.Join(target, name)
		if have, err := os.ReadFile(dest); err == nil && bytes.Equal(have, files[name]) {
			continue
		}
		// The shell library is sourced, and Makefile fragments are included;
		// neither needs the execute bit, and 0644 keeps the mode uniform
		// across repos so a mode flip never reads as drift.
		if err := os.WriteFile(dest, files[name], 0o644); err != nil { //nolint:gosec // a tracked shell library other users and CI must read; 0600 would break every non-owner invocation
			return nil, fmt.Errorf("write %s: %w", filepath.Join(LibDir, name), err)
		}
		changed = append(changed, name)
	}
	return changed, nil
}
