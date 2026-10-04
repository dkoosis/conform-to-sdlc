package checks

// Scaffold is the emit half of the checker. Every rule in this package has a
// verify half that reads a repo file and decides whether it satisfies the
// contract; the renderer below writes those same files FROM THE SAME
// package variables — checkFloor, floorEnable, requiredVerbs, bdKeys,
// trackedHookEvents, pinFile, bdConfigFile.
//
// That sharing is the whole point of building `conform-to-sdlc init` instead of
// adopting a copier template (decision conform-to-sdlc-init-build-thin, sd-th5.24):
// a template is a second copy of the contract and drifts from the checker
// the first time a rule moves. Here a rule change moves both halves at
// once, and scaffold_test.go proves it by mutating a checker variable and
// asserting the emitted file follows.
//
// The renderer is pure: it never touches the network, never reads machine
// state, and writes nothing outside the target directory. Machine and
// GitHub bootstrap live in bootstrap.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dkoosis/conform-to-sdlc/internal/values"
)

// defaultOwner is the GitHub owner every fleet repo lives under; it is the
// same owner --fleet sweeps.
const defaultOwner = fleetOwner

// defaultLintPin is the golangci-lint version a fresh repo pins. It is a
// starting value, not a contract: the lint-pin rule checks that exactly one
// pin exists, never which version it names.
const defaultLintPin = "v2.5.0"

// defaultGoVersion is the go directive a fresh go.mod carries.
const defaultGoVersion = "1.25"

// ErrScaffoldSpec marks a spec that cannot produce a conforming repo.
var ErrScaffoldSpec = errors.New("invalid scaffold spec")

// ErrScaffoldExists marks a target directory that already holds one of the
// files init would write. init scaffolds; it never overwrites.
var ErrScaffoldExists = errors.New("file exists")

// ScaffoldSpec is the per-repo input the renderer needs. Everything else the
// skeleton carries comes from the checker's own rule vocabulary.
type ScaffoldSpec struct {
	Repo    string         // repo name, e.g. "widget"
	Owner   string         // GitHub owner; defaults to the fleet owner
	Module  string         // go module path; defaults to github.com/<owner>/<repo>
	Prefix  string         // bd issue prefix, 2-3 letters
	PlanDir string         // bd custom.plan_dir
	Profile values.Profile // tool | lib
	LintPin string         // golangci-lint version for the single pin file
	GoVer   string         // go.mod go directive
}

// applyDefaults fills every blank a repo name can imply, so `conform-to-sdlc init
// <repo>` is a complete invocation.
func (s *ScaffoldSpec) applyDefaults() {
	if s.Owner == "" {
		s.Owner = defaultOwner
	}
	if s.Profile == "" {
		s.Profile = values.ProfileTool
	}
	if s.Repo == "" {
		return // validate() reports it; the rest cannot be derived
	}
	if s.Module == "" {
		s.Module = "github.com/" + s.Owner + "/" + s.Repo
	}
	if s.Prefix == "" {
		s.Prefix = defaultPrefix(s.Repo)
	}
	if s.PlanDir == "" {
		s.PlanDir = "~/Projects/dk/Project/" + s.Repo + "/plans"
	}
	if s.LintPin == "" {
		s.LintPin = defaultLintPin
	}
	if s.GoVer == "" {
		s.GoVer = defaultGoVersion
	}
}

// defaultPrefix takes the first three letters of the repo name — the 2-3
// letter shape the beads rule asks for.
func defaultPrefix(repo string) string {
	letters := make([]rune, 0, 3)
	for _, r := range strings.ToLower(repo) {
		if r >= 'a' && r <= 'z' {
			letters = append(letters, r)
		}
		if len(letters) == 3 {
			break
		}
	}
	return string(letters)
}

// validate rejects a spec that could not produce a conforming repo, before
// anything is written.
func (s *ScaffoldSpec) validate() error {
	switch {
	case s.Repo == "":
		return fmt.Errorf("%w: repo name is required", ErrScaffoldSpec)
	case strings.ContainsAny(s.Repo, `/\`) || strings.Contains(s.Repo, ".."):
		return fmt.Errorf("%w: repo name %q must be a bare name, not a path", ErrScaffoldSpec, s.Repo)
	case s.Profile != values.ProfileTool && s.Profile != values.ProfileLib:
		return fmt.Errorf("%w: %w: got %q", ErrScaffoldSpec, values.ErrInvalidProfile, s.Profile)
	case len(s.Prefix) < 2 || len(s.Prefix) > 3:
		return fmt.Errorf("%w: bd prefix %q must be 2-3 letters", ErrScaffoldSpec, s.Prefix)
	}
	return nil
}

// artifact is one emitted file: where it goes, how it renders, and whether
// git must see it as executable.
type artifact struct {
	path string
	mode os.FileMode
	// dirMode, when non-zero, is applied to the file's parent directory.
	// bd refuses a .beads it does not own privately.
	dirMode os.FileMode
	body    func(ScaffoldSpec) string
}

// baseArtifacts is the fixed half of the skeleton. Every path is a constant
// the matching verify half reads, so a rule that renames its file renames
// the emitted one too.
var baseArtifacts = []artifact{
	{path: ReadmeFile, mode: 0o644, body: renderReadme},
	{path: ValuesFile, mode: 0o644, body: renderValuesFile},
	{path: makefileFile, mode: 0o644, body: renderMakefile},
	{path: ".golangci.yml", mode: 0o644, body: renderGolangci},
	{path: pinFile, mode: 0o644, body: renderProjectConf},
	{path: ciGateFile, mode: 0o644, body: renderCheckWorkflow},
	{path: bdConfigFile, mode: 0o644, dirMode: 0o700, body: renderBDConfig},
	{path: "go.mod", mode: 0o644, body: renderGoMod},
	{path: "doc.go", mode: 0o644, body: renderDoc},
	{path: RoadmapFile, mode: 0o644, body: renderRoadmap},
	{path: VocabularyFile, mode: 0o644, body: renderVocabulary},
}

// scaffoldArtifacts is the skeleton, one entry per file. The paths are the
// same constants the verify halves read, so a rule that renames its file
// renames the emitted one too.
func scaffoldArtifacts() []artifact {
	arts := make([]artifact, 0, len(baseArtifacts)+len(trackedHookEvents))
	arts = append(arts, baseArtifacts...)
	// The hook family: one file per event the local surface tracks, each
	// delegating to bd by the marker checkBDHooks looks for.
	for _, event := range trackedHookEvents {
		arts = append(arts, artifact{
			path: filepath.Join(hooksDir, event),
			mode: 0o755,
			body: renderHook(event),
		})
	}
	return arts
}

// Scaffold writes the skeleton for spec into dir. It refuses to overwrite:
// if any artifact already exists, nothing is written and the call fails.
// Nothing is written outside dir, and nothing touches the network.
func Scaffold(dir string, spec ScaffoldSpec) error {
	spec.applyDefaults()
	if err := spec.validate(); err != nil {
		return err
	}
	arts := scaffoldArtifacts()

	// Refuse before writing, so a partial skeleton never lands on a repo
	// that already had files.
	for _, a := range arts {
		if _, err := os.Stat(filepath.Join(dir, a.path)); err == nil {
			return fmt.Errorf("%w: %s — init scaffolds a new repo, it does not patch an existing one", ErrScaffoldExists, a.path)
		}
	}

	for _, a := range arts {
		if err := writeArtifact(dir, a, spec); err != nil {
			return err
		}
	}
	return nil
}

// writeArtifact renders one artifact into dir, setting the modes the rules
// read (git ignores a non-executable hook; bd refuses a world-readable
// .beads).
func writeArtifact(dir string, a artifact, spec ScaffoldSpec) error {
	full := filepath.Join(dir, a.path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(a.body(spec)), a.mode); err != nil {
		return err
	}
	// WriteFile honors umask, so set the bits explicitly.
	if err := os.Chmod(full, a.mode); err != nil {
		return err
	}
	if a.dirMode == 0 {
		return nil
	}
	return os.Chmod(filepath.Dir(full), a.dirMode)
}

// ScaffoldPaths lists what Scaffold would write, in order. `conform-to-sdlc init`
// prints it; nothing else depends on it.
func ScaffoldPaths(spec ScaffoldSpec) []string {
	spec.applyDefaults()
	arts := scaffoldArtifacts()
	paths := make([]string, 0, len(arts))
	for _, a := range arts {
		paths = append(paths, a.path)
	}
	return paths
}

// ── renderers ────────────────────────────────────────────────────────────
// Each renderer below is the emit half of exactly one rule, and reads the
// same variable its verify half reads.

// renderValuesFile emits docs/conform.json through the values package the loader
// parses it with — one schema, marshalled and unmarshalled by the same type.
func renderValuesFile(spec ScaffoldSpec) string {
	v := values.Values{Profile: spec.Profile, Exceptions: []values.Exception{}}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil { // values.Values is plain data; MarshalIndent cannot fail
		panic(err)
	}
	return string(data) + "\n"
}

// renderMakefile emits the four-verb contract. The verb list comes from
// requiredVerbs (shared with verbFindings) and `check`'s prerequisites come
// from checkFloor (shared with verbFindings) — so the emitted Makefile is
// the minimum the rule accepts, by construction rather than by copy.
func renderMakefile(spec ScaffoldSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s Makefile — the fleet four-verb contract.\n", spec.Repo)
	b.WriteString("# Generated by `conform-to-sdlc init`; conform-to-sdlc checks it on every `make check`.\n\n")
	b.WriteString(".DEFAULT_GOAL := check\n\n")
	b.WriteString("SHELL := /bin/bash\n")
	b.WriteString(".SHELLFLAGS := -euo pipefail -c\n\n")

	phony := append(requiredVerbs(spec.Profile), floorTargets(spec.Profile)...)
	fmt.Fprintf(&b, ".PHONY: %s\n\n", strings.Join(phony, " "))

	// The verbs, in contract order. help is rendered from the same list it
	// documents, so a new verb cannot go undocumented.
	verbs := requiredVerbs(spec.Profile)
	fmt.Fprintf(&b, "help: ## Show this help — the %d verbs, identical in every dkoosis repo\n", len(verbs))
	b.WriteString("\t@awk 'BEGIN {FS = \":.*##\"} /^[a-zA-Z0-9_.-]+:.*?## / { printf \"  \\033[36m%-14s\\033[0m %s\\n\", $$1, $$2 }' $(MAKEFILE_LIST)\n\n")

	fmt.Fprintf(&b, "check: %s selfcheck ## Fast gate — %s, then conform-to-sdlc last. Pre-commit; required in CI.\n",
		strings.Join(checkFloor, " "), strings.Join(checkFloor, " + "))
	b.WriteString("\t@echo \"=== check pass ===\"\n\n")

	b.WriteString("audit: check race vuln ## Exhaustive validation — the gate, plus race and vulnerabilities\n")
	b.WriteString("\t@echo \"=== audit pass ===\"\n\n")

	if slices.Contains(verbs, "deploy") {
		b.WriteString("deploy: install ## Build, then install this tool locally\n")
		b.WriteString("\t@echo \"=== deploy pass ===\"\n\n")
		b.WriteString("install: build ## Install this tool into GOBIN\n")
		fmt.Fprintf(&b, "\tgo install ./cmd/%s\n\n", spec.Repo)
		b.WriteString("cross: ## Cross-compile linux-amd64 and linux-arm64 into .sandbox/bin/\n")
		fmt.Fprintf(&b, "\tfor a in amd64 arm64; do mkdir -p .sandbox/bin/linux-$$a && CGO_ENABLED=0 GOOS=linux GOARCH=$$a go build -o .sandbox/bin/linux-$$a/ ./cmd/%s; done\n\n", spec.Repo)
	}

	b.WriteString("selfcheck: ## Run conform-to-sdlc (fleet SDLC checker) against this repo\n")
	b.WriteString("\tconform-to-sdlc\n\n")
	b.WriteString("clean: ## Remove build outputs\n")
	b.WriteString("\trm -rf bin .sandbox/bin\n\n")

	// The internal steps check composes. Rendering them from checkFloor
	// keeps the two in step: a floor entry with no target would fail the
	// build, and a target with no ## doc would fail makefile-docs.
	for _, step := range checkFloor {
		fmt.Fprintf(&b, "%s: ## %s\n\t%s\n\n", step, mkStepDoc(step), mkStepRecipe(step))
	}

	b.WriteString("race: ## Run tests with the race detector (fresh run)\n")
	b.WriteString("\tgo test -race -count=1 -cover ./...\n\n")
	b.WriteString("vuln: ## Scan for known vulnerabilities\n")
	b.WriteString("\tgovulncheck ./...\n")
	return b.String()
}

// mkStepDoc and mkStepRecipe supply a body for each checkFloor entry. An
// entry with no known recipe still gets a documented placeholder target, so
// adding one to the floor never emits a Makefile that fails makefile-docs.
func mkStepDoc(step string) string {
	switch step {
	case "vet":
		return "Run go vet"
	case "lint":
		return "Run golangci-lint (version pinned in " + pinFile + ")"
	case "test":
		return "Run tests with coverage"
	case "build":
		return "Compile everything"
	default:
		return "Internal step of check — fill in " + step
	}
}

func mkStepRecipe(step string) string {
	switch step {
	case "vet":
		return "go vet ./..."
	case "lint":
		return "golangci-lint run ./..."
	case "test":
		return "go test -cover ./..."
	case "build":
		return "go build ./..."
	default:
		return "@echo \"TODO: implement " + step + "\""
	}
}

// renderGolangci emits the lint floor from floorEnable — the same slice
// checkLintFloor demands — plus the nolintlint settings that rule requires.
func renderGolangci(ScaffoldSpec) string {
	var b strings.Builder
	b.WriteString("# Fleet lint floor. Generated by `conform-to-sdlc init`; the lint-floor rule\n")
	b.WriteString("# checks this file as parsed sets, so per-repo extras are free.\n")
	b.WriteString("version: \"2\"\n\n")
	b.WriteString("linters:\n")
	b.WriteString("  default: standard\n")
	b.WriteString("  enable:\n")
	for _, l := range floorEnable {
		fmt.Fprintf(&b, "    - %s\n", l)
	}
	b.WriteString("  settings:\n")
	b.WriteString("    nolintlint:\n")
	b.WriteString("      require-explanation: true\n")
	b.WriteString("      require-specific: true\n")
	return b.String()
}

// renderProjectConf emits the one pin location checkLintPin reads. No other
// emitted file may carry a golangci version literal — the same rule that
// forbids it fleet-wide forbids it here.
func renderProjectConf(spec ScaffoldSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — the single fleet pin location. Nothing else may carry a\n", pinFile)
	b.WriteString("# golangci-lint version literal; conform-to-sdlc's lint-pin rule enforces that.\n")
	fmt.Fprintf(&b, "PROJECT_NAME=%s\n", spec.Repo)
	fmt.Fprintf(&b, "GOLANGCI_LINT_VERSION=%s\n", spec.LintPin)
	return b.String()
}

// renderCheckWorkflow emits CI that calls the make verb and re-implements
// none of it — exactly what checkCIGate demands — behind a detect job that
// lets a docs-only pull request skip the gate while still reporting the
// check context (checkCIDocsSkip). The ignore regex renders from
// docsOnlyPaths, its one home.
func renderCheckWorkflow(ScaffoldSpec) string {
	return strings.NewReplacer("@DOCS@", renderDocsOnlyRegex(), "@PIN@", pinFile).Replace(checkWorkflowTmpl)
}

// checkWorkflowTmpl is renderCheckWorkflow's body; @DOCS@ and @PIN@ fill in
// from their one homes.
const checkWorkflowTmpl = `name: check

# Always triggers — never paths/paths-ignore. A skipped workflow never reports
# the required check context, so a docs-only PR could not merge under branch
# protection. The detect job decides docs-only; check skips its steps and
# still reports green. Any changed path outside the docs set runs the full gate.
on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

concurrency:
  group: check-${{ github.ref }}
  cancel-in-progress: true

jobs:
  detect:
    runs-on: ubuntu-latest
    outputs:
      run_check: ${{ steps.diff.outputs.run_check }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - id: diff
        run: |
          set -euo pipefail
          if [ "${{ github.event_name }}" = "pull_request" ]; then
            base="${{ github.event.pull_request.base.sha }}"
            head="${{ github.event.pull_request.head.sha }}"
          else
            base="${{ github.event.before }}"
            head="${{ github.sha }}"
          fi
          # Unknown base (new branch, all-zero SHA) → run the gate.
          if ! git cat-file -e "${base}^{commit}" 2>/dev/null; then
            echo "run_check=true" >> "$GITHUB_OUTPUT"
            exit 0
          fi
          # The docs-only set is conform-to-sdlc's (ci-docs-skip): drop entries to
          # narrow it; adding one is a finding.
          run_check="$(git diff --name-status --no-renames "$base" "$head" | awk -F'\t' '
            NF < 2 { next }
            $2 !~ @DOCS@ { print "true"; exit }
          ')"
          echo "run_check=${run_check:-false}" >> "$GITHUB_OUTPUT"

  check:
    needs: detect
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - name: docs-only — skip the gate
        if: needs.detect.outputs.run_check != 'true'
        run: echo "Only docs changed — reporting check without running the gate."

      - uses: actions/checkout@v4
        if: needs.detect.outputs.run_check == 'true'

      - uses: actions/setup-go@v5
        if: needs.detect.outputs.run_check == 'true'
        with:
          go-version-file: go.mod
          check-latest: false

      - name: install linter (version pinned in @PIN@)
        if: needs.detect.outputs.run_check == 'true'
        run: |
          . ./@PIN@
          curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
            | sh -s -- -b "$(go env GOPATH)/bin" "$GOLANGCI_LINT_VERSION"
          echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"

      # CI runs the developer's gate. No step here duplicates what a make
      # target already owns — that split is what the ci-gate rule protects.
      - name: check
        if: needs.detect.outputs.run_check == 'true'
        run: make check
`

// renderBDConfig emits the tracked bd declaration from bdKeys — the same
// slice checkBDConfig walks. A fourth required key added to that slice lands
// in this file without touching the renderer.
func renderBDConfig(spec ScaffoldSpec) string {
	var b strings.Builder
	b.WriteString("# Tracked bd declaration. Surface 1 reads this file; --local\n")
	b.WriteString("# compares it against the live bd config on this machine.\n")
	for _, k := range bdKeys {
		fmt.Fprintf(&b, "%s: %q\n", k.paths[0], bdConfigValue(k.key, spec))
	}
	return b.String()
}

// bdConfigValue supplies the per-repo value for one declared bd key.
func bdConfigValue(key string, spec ScaffoldSpec) string {
	switch key {
	case "custom.plan_dir":
		return spec.PlanDir
	case "issue_prefix":
		return spec.Prefix
	case "sync.remote":
		return fmt.Sprintf("git+https://github.com/%s/%s.git", spec.Owner, spec.Repo)
	default:
		// A key added to bdKeys with no value here still gets declared, and
		// with a literal TODO rather than an empty string that would read as
		// "declared" to the checker and as done to a human.
		return "TODO: " + key
	}
}

// renderHook emits one shape-B hook: a tracked source file that delegates to
// bd's machinery through the marker delegatesToBD looks for.
func renderHook(event string) func(ScaffoldSpec) string {
	return func(ScaffoldSpec) string {
		return fmt.Sprintf(`#!/usr/bin/env bash
# %s — tracked hook (shape B). core.hooksPath points at %s; `+"`conform-to-sdlc --local`"+`
# checks that it does.
set -euo pipefail

# BEGIN %s
# Hand the event to bd's shim when this clone is hydrated. A clone without
# bd installed skips the delegation rather than blocking the commit.
if [ -x "%s/%s" ]; then
  "%s/%s" "$@"
fi
# END %s
`, event, hooksDir, delegationMarker, delegationShim, event, delegationShim, event, delegationMarker)
	}
}

func renderGoMod(spec ScaffoldSpec) string {
	return fmt.Sprintf("module %s\n\ngo %s\n", spec.Module, spec.GoVer)
}

// renderDoc gives `go build ./...` and `go vet ./...` a package to chew on,
// so `make check` is runnable the moment the skeleton lands.
// renderRoadmap emits the direction home. It is the one artifact whose body a
// human must replace before it says anything, so it ships the init variant of
// the shared page — a ★ line the checker accepts and a reader can see is
// unwritten (checks.RoadmapScaffold).
func renderRoadmap(spec ScaffoldSpec) string {
	return RoadmapScaffold(spec.Repo)
}

// renderReadme emits the one file the root must carry, through the same
// renderer the readme rule's --fix half uses (checks.ReadmeScaffold).
func renderReadme(spec ScaffoldSpec) string {
	return ReadmeScaffold(spec.Repo)
}

func renderDoc(spec ScaffoldSpec) string {
	return fmt.Sprintf("// Package %s is the root of the %s module.\n//\n// Scaffolded by `conform-to-sdlc init`. Replace this file with real code.\npackage %s\n",
		goIdent(spec.Repo), spec.Repo, goIdent(spec.Repo))
}

// renderVocabulary emits the starting .claude/rules/vocabulary.md: a place
// for the repo's own domain words, not content the checker reads (it only
// checks the file is present).
func renderVocabulary(ScaffoldSpec) string {
	return `# Vocabulary

This repo's own domain words — one definition each. Not sdlc's process
vocabulary; that is a separate, shared standard.
`
}

// goIdent turns a repo name into a legal package identifier.
func goIdent(repo string) string {
	out := make([]rune, 0, len(repo))
	for _, r := range strings.ToLower(repo) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9' && len(out) > 0) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "main"
	}
	return string(out)
}
