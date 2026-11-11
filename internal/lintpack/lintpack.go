// Package lintpack carries the lintbrush rules pack: the ruleguard rules every
// fleet repo holds in .golangci-rules/, plus the golangci wiring they ship with.
//
// Its canon is dkoosis/lintbrush, gorules/. Each repo used to copy the pack and
// run check-pack-drift.sh against an upstream URL; when that URL died the
// script warned and exited 0, so nothing checked the copies and they drifted.
// repo-standard now embeds the pack, as it embeds .sandbox/lib, and compares
// each repo's copy byte for byte with no network call. The repo-standard
// version a repo pins is its pack version.
//
// _gorules/ is refreshed only by `make pack-refresh`, which reads a lintbrush
// commit and records it, with each file's git blob id, in _gorules/SOURCE.
// The leading underscore keeps the Go rule files out of this module's build.
package lintpack

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed _gorules
var assets embed.FS

// Dir is the repo-relative directory a repo keeps its copy of the pack in.
const Dir = ".golangci-rules"

const assetDir = "_gorules"

// A File is one member of the pack: its name in lintbrush gorules/, its name in
// a repo's .golangci-rules/, and whether every repo must carry it.
type File struct {
	Source   string // name in lintbrush gorules/
	Repo     string // name in .golangci-rules/; empty for files a repo does not hold
	Required bool
}

// Members lists the pack. rules.go is named bugclasses.go in the repos.
// optin_rawstatewrite.go is opt-in (rule 2): a repo may leave it out, and
// when it holds it, it must match. The two YAML files are the golangci wiring a
// repo merges into its .golangci.yml; repos do not keep them in
// .golangci-rules/.
var Members = []File{
	{Source: "rules.go", Repo: "bugclasses.go", Required: true},
	{Source: "modern.go", Repo: "modern.go", Required: true},
	{Source: "optin_rawstatewrite.go", Repo: "optin_rawstatewrite.go"},
	{Source: "golangci.snippet.yml"},
	{Source: "golangci.baseline.yml"},
}

// Bytes returns the embedded content of the pack file named source, as named
// in lintbrush gorules/.
func Bytes(source string) ([]byte, error) {
	b, err := assets.ReadFile(path.Join(assetDir, source))
	if err != nil {
		return nil, fmt.Errorf("read embedded pack file %s: %w", source, err)
	}
	return b, nil
}

// Source is the provenance _gorules/SOURCE records: the lintbrush commit the
// embed was copied from and the git blob id of each file at that commit.
type Source struct {
	Commit string
	Blobs  map[string]string // source name → git blob id
}

// ErrSource marks a _gorules/SOURCE that does not parse.
var ErrSource = errors.New("lintpack: bad _gorules/SOURCE")

// ReadSource parses _gorules/SOURCE.
func ReadSource() (Source, error) {
	data, err := assets.ReadFile(path.Join(assetDir, "SOURCE"))
	if err != nil {
		return Source{}, fmt.Errorf("read embedded SOURCE: %w", err)
	}
	src := Source{Blobs: map[string]string{}}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			return Source{}, fmt.Errorf("%w: malformed line %q", ErrSource, line)
		}
		if key == "commit" {
			src.Commit = val
			continue
		}
		src.Blobs[val] = key
	}
	if src.Commit == "" {
		return Source{}, fmt.Errorf("%w: no commit line", ErrSource)
	}
	return src, nil
}

// Sync writes the pack into dir/.golangci-rules, overwriting drift. It writes
// every required file, and an opt-in file only when the repo already holds it,
// so syncing never turns an opt-in rule on. Files repo-standard does not own
// are left alone. It reports the repo names it changed.
func Sync(dir string) ([]string, error) {
	target := filepath.Join(dir, Dir)
	if err := os.MkdirAll(target, 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", Dir, err)
	}
	var changed []string
	for _, m := range Members {
		if m.Repo == "" {
			continue
		}
		dest := filepath.Join(target, m.Repo)
		have, err := os.ReadFile(dest)
		if !m.Required && err != nil {
			continue
		}
		want, err := Bytes(m.Source)
		if err != nil {
			return changed, err
		}
		if bytes.Equal(have, want) {
			continue
		}
		if err := os.WriteFile(dest, want, 0o600); err != nil {
			return changed, fmt.Errorf("write %s: %w", filepath.Join(Dir, m.Repo), err)
		}
		changed = append(changed, m.Repo)
	}
	return changed, nil
}

// Create writes the required pack files into dir/.golangci-rules only when
// that path does not exist at all. It builds the directory beside the target
// and renames it into place, so a failed write leaves nothing behind. It
// reports whether it wrote.
func Create(dir string) (bool, error) {
	target := filepath.Join(dir, Dir)
	if _, err := os.Lstat(target); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", Dir, err)
	}
	tmp, err := os.MkdirTemp(dir, Dir+".tmp-")
	if err != nil {
		return false, fmt.Errorf("create a staging directory for %s: %w", Dir, err)
	}
	for _, m := range Members {
		if !m.Required {
			continue
		}
		body, err := Bytes(m.Source)
		if err == nil {
			if err = os.WriteFile(filepath.Join(tmp, m.Repo), body, 0o600); err != nil {
				err = fmt.Errorf("write %s/%s: %w", Dir, m.Repo, err)
			}
		}
		if err != nil {
			_ = os.RemoveAll(tmp) // best-effort cleanup; the write error is the one returned
			return false, err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.RemoveAll(tmp) // best-effort cleanup; the rename error is the one returned
		return false, fmt.Errorf("move the staged pack into %s: %w", Dir, err)
	}
	return true, nil
}
