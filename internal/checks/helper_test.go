package checks_test

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/dkoosis/repo-standard/internal/checks"
	"github.com/dkoosis/repo-standard/internal/lintpack"
)

// goodMakefile satisfies the four-verb contract for the tool profile.
const goodMakefile = `.DEFAULT_GOAL := check
GOLANGCILINT := bash scripts/lint-locked

check: vet lint test build standard ## Fast validation: vet + lint + test + build, then repo-standard
audit: check race fuzz dupe nilcheck deadcode omitempty vuln ## Exhaustive validation
deploy: build ## Install locally
help: ## Show this help
vet: ## Run go vet
lint: ## Run golangci-lint
test: ## Run tests
build: ## Compile
race: ## Race detector
vuln: ## Vulnerability scan
fuzz: ## Fuzz
dupe: ## Duplication
nilcheck: ## nilaway
deadcode: ## Unreachable functions
omitempty: ## bool omitempty
standard: ## Run repo-standard
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

const goodAuditYML = `name: audit
on:
  schedule:
    - cron: '41 7 * * *'
jobs:
  audit:
    runs-on: ubuntu-latest
    steps:
      - run: make audit
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
GOLANGCI_LINT_VERSION=v2.14.0
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
	files := map[string]string{
		"docs/repo-standard.json":            goodValues,
		".beads/config.yaml":                 goodBDConfig,
		"Makefile":                           goodMakefile,
		".golangci.yml":                      checks.CanonicalGolangci,
		".sandbox/project.conf":              goodProjectConf,
		".github/workflows/check.yml":        goodCheckYML,
		".github/workflows/audit.yml":        goodAuditYML,
		".github/workflows/codex-review.yml": goodCodexYML,
		"README.md":                          "# repo\n\nwhat this repo is, in one paragraph.\n",
		".githooks/pre-commit":               "#!/bin/sh\nexit 0\n",
		".githooks/pre-push":                 "#!/bin/sh\nexit 0\n",
	}
	maps.Copy(files, packFiles())
	return files
}

// packFiles is the required lint rules pack, as a repo holds it.
func packFiles() map[string]string {
	files := map[string]string{}
	for _, m := range lintpack.Members {
		if !m.Required {
			continue
		}
		body, err := lintpack.Bytes(m.Source)
		if err != nil {
			panic(err)
		}
		files[lintpack.Dir+"/"+m.Repo] = string(body)
	}
	return files
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
