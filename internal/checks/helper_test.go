package checks_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// goodMakefile satisfies the four-verb contract for the tool profile.
const goodMakefile = `.DEFAULT_GOAL := check
GOLANGCILINT := bash scripts/lint-locked

check: vet lint test build selfcheck ## Fast validation: vet + lint + test + build, then conform-to-sdlc
audit: check race ## Exhaustive validation
deploy: build ## Install locally
help: ## Show this help
vet: ## Run go vet
lint: ## Run golangci-lint
test: ## Run tests
build: ## Compile
race: ## Race detector
vuln: ## Vulnerability scan
selfcheck: ## Run conform-to-sdlc
clean: ## Remove build outputs
install: ## Install into GOBIN
cross: ## Cross-compile linux-amd64 and linux-arm64
`

// goodGolangci carries the baseline floor and nothing that trips it.
const goodGolangci = `version: "2"
linters:
  default: standard
  enable:
    - nilerr
    - rowserrcheck
    - noctx
    - staticcheck
    - nolintlint
  settings:
    nolintlint:
      require-explanation: true
      require-specific: true
`

const goodCheckYML = `name: check
on:
  pull_request:
jobs:
  detect:
    runs-on: ubuntu-latest
    outputs:
      run_check: ${{ steps.diff.outputs.run_check }}
    steps:
      - id: diff
        run: |
          run_check="$(git diff --name-status "$base" "$head" | awk -F'\t' '
            $2 !~ /(\.md$|^docs\/|^\.beads\/|^\.claude\/|^\.gitignore$|^LICENSE$)/ { print "true"; exit }
          ')"
          echo "run_check=${run_check:-false}" >> "$GITHUB_OUTPUT"
  check:
    needs: detect
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: golangci-lint (pinned in .sandbox/project.conf)
        run: |
          . ./.sandbox/project.conf
          install-golangci "$GOLANGCI_LINT_VERSION"
      - name: check
        if: needs.detect.outputs.run_check == 'true'
        run: make check
      - name: race
        run: make race
`

const goodCodexYML = `name: codex-review
on:
  issue_comment:
    types: [created]
jobs:
  codex:
    runs-on: ubuntu-latest
    steps:
      - run: echo review
`

const goodProjectConf = `PROJECT_NAME=x
GOLANGCI_LINT_VERSION=v2.12.2
`

const goodValues = `{"profile": "tool", "exceptions": []}` + "\n"

// goodBDConfig declares the three fleet bd keys in the tracked file
// (nested sync shape, flat custom shape — both fleet-real).
const goodBDConfig = `issue-prefix: "cfm"
custom.plan_dir: /vault/plans
sync:
    remote: "git+https://github.com/x/y.git"
`

// goodRepo is a complete conforming tool repo, minus bd state (tests stub bd
// on PATH via fakeBD).
func goodRepo() map[string]string {
	return map[string]string{
		"docs/conform.json":                  goodValues,
		".beads/config.yaml":                 goodBDConfig,
		"Makefile":                           goodMakefile,
		".golangci.yml":                      goodGolangci,
		".sandbox/project.conf":              goodProjectConf,
		".github/workflows/check.yml":        goodCheckYML,
		".github/workflows/codex-review.yml": goodCodexYML,
		"README.md":                          "# repo\n\nwhat this repo is, in one paragraph.\n",
		".githooks/pre-commit":               "#!/bin/sh\nexit 0\n",
		".githooks/pre-push":                 "#!/bin/sh\nexit 0\n",
		checks.VocabularyFile:                "",
	}
}

// writeRepo materializes files (path → content) under a fresh temp dir.
// Paths under .githooks/ are written executable.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if filepath.Dir(rel) == ".githooks" {
			mode = 0o755
		}
		if err := os.WriteFile(abs, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// rulesOf collects the distinct rule ids present in findings.
func rulesOf(findings []checks.Finding) map[string]int {
	rules := make(map[string]int)
	for _, f := range findings {
		rules[f.Rule]++
	}
	return rules
}

// findingFor returns the single finding for rule, failing the test otherwise.
func findingFor(t *testing.T, findings []checks.Finding, rule string) checks.Finding {
	t.Helper()
	var hits []checks.Finding
	for _, f := range findings {
		if f.Rule == rule {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one %s finding, got %d (all findings: %v)", rule, len(hits), findings)
	}
	return hits[0]
}
