package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The single fleet pin for golangci-lint lives in .sandbox/project.conf as
// GOLANGCI_LINT_VERSION. CI sources that file; nothing else may carry a
// literal version — up to four pin files per repo, three self-contradictory,
// is the drift this rule exists to kill.
const pinFile = ".sandbox/project.conf"

var (
	pinKey = regexp.MustCompile(`(?m)^GOLANGCI_LINT_VERSION=v?[0-9]`)
	// strayPin: a golangci mention and a version literal on one line,
	// outside the pin file.
	versionLit = regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+`)
)

// checkLintPin verifies the pin exists in project.conf and appears nowhere
// else (lint-pin).
func checkLintPin(dir string) []Finding {
	var findings []Finding

	data, err := os.ReadFile(filepath.Join(dir, pinFile))
	if err != nil || !pinKey.Match(data) {
		findings = append(findings, Finding{
			File:   pinFile,
			Rule:   RuleLintPin,
			Msg:    "GOLANGCI_LINT_VERSION not pinned — CI and the sandbox must read one key from one file",
			Repair: `add GOLANGCI_LINT_VERSION=v<x.y.z> to .sandbox/project.conf`,
		})
	}

	return append(findings, strayPinFindings(dir)...)
}

// strayPinFindings sweeps the places stray pins have historically lived —
// recursing the whole tree would blow the <1s budget for no additional
// coverage — and reports any golangci version literal outside the pin file.
func strayPinFindings(dir string) []Finding {
	candidates := []string{makefileFile, "tools.go"}
	for _, glob := range []string{".github/workflows/*.yml", ".github/workflows/*.yaml", "scripts/*"} {
		matches, _ := filepath.Glob(filepath.Join(dir, glob)) //nolint:errcheck // Glob errs only on a malformed pattern; these are constant
		for _, m := range matches {
			rel, err := filepath.Rel(dir, m)
			if err != nil {
				continue
			}
			candidates = append(candidates, rel)
		}
	}

	var findings []Finding
	for _, rel := range candidates {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.Contains(strings.ToLower(line), "golangci") && versionLit.MatchString(line) {
				findings = append(findings, Finding{
					File:   rel,
					Rule:   RuleLintPin,
					Msg:    fmt.Sprintf("second golangci-lint pin: %q — the fleet pin lives only in %s", strings.TrimSpace(line), pinFile),
					Repair: "read $GOLANGCI_LINT_VERSION from " + pinFile + " instead of a literal",
				})
				break // one finding per file is enough to act on
			}
		}
	}
	return findings
}
