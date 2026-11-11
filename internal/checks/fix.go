package checks

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dkoosis/conform-to-sdlc/internal/lintpack"
)

// Fix applies the Surface-1 repairs conform-to-sdlc can make without judgment, and
// returns one line per action taken. A clean repo returns nothing.
//
// The bar for living here is narrow on purpose: the repair must be the ONLY
// correct one, and it must never destroy work. Creating a file that is absent
// qualifies. Rewriting one that exists does not — conform-to-sdlc would be guessing at
// content a human wrote, and a checker that edits your prose stops being
// trusted long before it stops being right. Everything else stays a Repair
// string on the finding, for a person to run.
//
// Fix never overwrites, so running it twice is safe and the second run is
// silent.
//
// One repair failing does not stop the others: each touches its own file, and
// the errors come back joined.
func Fix(dir string) ([]string, error) {
	var (
		done []string
		errs []error
	)

	for _, fix := range []func(string) (string, error){fixReadme, fixCheckWorkflow, fixLintPack, fixGolangci} {
		created, err := fix(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if created != "" {
			done = append(done, created)
		}
	}

	return done, errors.Join(errs...)
}

// fixGolangci writes the canonical .golangci.yml when the repo has none. A
// present file is never touched, however far it is from the set: its extras
// and exclusions are a person's, and lint-set names what it lacks.
func fixGolangci(dir string) (string, error) {
	path := filepath.Join(dir, golangciFile)
	if golangciOtherName(dir) != "" {
		return "", nil // the repo has a config under another name; lint-set names it
	}
	if _, err := os.Lstat(path); err == nil {
		return "", nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", golangciFile, err)
	}
	// O_EXCL: never replace a file that appeared since the check above.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", golangciFile, err)
	}
	if _, err := f.WriteString(CanonicalGolangci); err != nil {
		_ = f.Close()       // the write error is the one returned
		_ = os.Remove(path) // leave no half-written config behind
		return "", fmt.Errorf("write %s: %w", golangciFile, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write %s: %w", golangciFile, err)
	}
	return fmt.Sprintf("created %s — the canonical linter set; tune its path exclusions to this repo's layout", golangciFile), nil
}

// fixLintPack writes the required lintbrush pack files when the repo has no
// .golangci-rules/ at all. A present directory is never touched, even when a
// file in it differs or is missing: the file may hold a person's edit, and
// lint-pack names it with the repair to run.
func fixLintPack(dir string) (string, error) {
	created, err := lintpack.Create(dir)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", lintpack.Dir, err)
	}
	if !created {
		return "", nil
	}
	return fmt.Sprintf(`created %s — the lintbrush rules pack conform-to-sdlc ships; enable gocritic's ruleguard check in .golangci.yml with rules: "${base-path}/.golangci-rules/bugclasses.go,${base-path}/.golangci-rules/modern.go"`, lintpack.Dir), nil
}

// fixReadme writes a README.md skeleton when the repo root carries none.
//
// The skeleton deliberately does not satisfy the readme rule: its first line
// with content is a comment, so the repo stays red until a person writes the
// heading. A --fix that turned the gate green would be reporting that someone
// had introduced the repo when nobody had.
func fixReadme(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	path := filepath.Join(abs, ReadmeFile)
	if _, err := os.Stat(path); err == nil {
		return "", nil // present — its heading and prose are a person's business
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", ReadmeFile, err)
	}
	// 0o600: the file is world-readable the moment git tracks it, so a wider
	// mode here buys nothing and trips gosec. #nosec is a worse answer than a
	// mode that is simply correct.
	if err := os.WriteFile(path, []byte(ReadmeSkeleton(repoName(abs))), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", ReadmeFile, err)
	}
	return fmt.Sprintf("created %s — replace the opening comment with `# %s`, then say what the repo is", ReadmeFile, repoName(abs)), nil
}

// fixCheckWorkflow writes .github/workflows/check.yml when the repo has none:
// make check behind the docs-only detect job, the shape ci-gate and
// ci-docs-skip read. An existing workflow is never touched — its extra steps
// are a person's to port.
//
// Unlike the README skeleton this file is not left red: a CI
// workflow carries no prose a person must still write, and the rendered file
// is the whole repair.
func fixCheckWorkflow(dir string) (string, error) {
	path := filepath.Join(dir, ciGateFile)
	if _, err := os.Stat(path); err == nil {
		return "", nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", ciGateFile, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(ciGateFile), err)
	}
	// 0o600 for the same reason fixReadme uses it.
	if err := os.WriteFile(path, []byte(renderCheckWorkflow(ScaffoldSpec{})), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", ciGateFile, err)
	}
	return fmt.Sprintf("created %s — make check behind the docs-only detect job", ciGateFile), nil
}

// repoName is the last element of the module path in dir's go.mod, falling
// back to the directory name. The directory alone is wrong in a worktree:
// --fix run there titled the wrong README after the worktree.
func repoName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if mod, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
				return path.Base(strings.Trim(strings.TrimSpace(mod), `"`))
			}
		}
	}
	return filepath.Base(dir)
}
