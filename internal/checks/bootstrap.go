package checks

// Bootstrap is the L2 half of `conform-to-sdlc init`: the machine and GitHub state a
// scaffolded repo needs before `conform-to-sdlc --local` and `conform-to-sdlc --fleet` go
// green. Surface 1 is files, and Scaffold writes them; this file writes what
// no file can carry.
//
// The split that matters here is local vs remote. Local steps (git init,
// core.hooksPath, bd init, bd config) touch only the target directory and
// this machine, so init runs them. Remote steps (labels, branch protection,
// merge policy) create state in a real GitHub account, so they are INERT by
// default: init prints them and runs them only when the caller passes
// --with-remote. Tests exercise the command construction, never the
// execution.
//
// Every command below is the repair string of the rule it satisfies —
// checkHooksPath's `git config core.hooksPath`, labelFindings' `gh label
// create`, protectionFindings' protection PUT, mergePolicyFindings' PATCH.
// One vocabulary, whether conform-to-sdlc is reporting a gap or closing it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// bootstrapTimeout bounds one local bootstrap command. bd init on a cold
// machine hydrates an embedded Dolt store, which is slower than the 2s the
// Surface-2 probes allow.
const bootstrapTimeout = 60 * ghTimeout / 15 // 60s

// ErrBootstrap marks a bootstrap step that failed.
var ErrBootstrap = errors.New("bootstrap")

// Step is one bootstrap command: what it is for, the argv, and whether it
// reaches the network.
type Step struct {
	Why    string   // the rule or wiring this closes
	Argv   []string // command and arguments
	Remote bool     // creates state outside this machine
	// Optional marks a step whose failure is reported and survived. bd's
	// steps are optional because the tracked sync.remote names a GitHub
	// repo that need not exist yet — a clone failure there must not strand
	// a scaffolded repo half-wired.
	Optional bool
}

// String renders the step as the shell line a human would type.
func (s Step) String() string {
	return strings.Join(s.Argv, " ")
}

// BootstrapPlan is the ordered list of steps for a spec, local first. It is
// pure: building a plan runs nothing.
func BootstrapPlan(spec ScaffoldSpec) []Step {
	spec.applyDefaults()
	full := spec.Owner + "/" + spec.Repo
	steps := []Step{
		{
			Why:  "a repo with no git dir has nowhere to put hooks",
			Argv: []string{"git", "init", "-b", defaultBranch},
		},
		{
			// bd init runs `git commit` internally. Wiring core.hooksPath
			// before that commit would fire the tracked pre-commit hook,
			// which delegates to the .beads/hooks shim bd init itself is
			// mid-install of — a nested `bd` call that contends with this
			// still-running `bd init` for the same Dolt lock and hangs
			// until the bootstrap timeout kills it (cfm-d4r). Wiring hooks
			// after gives bd's own commit git's default (empty) hooks.
			//
			// --skip-agents: bd's own default init writes AGENTS.md,
			// CLAUDE.md, .claude/settings.json and a Codex skill install —
			// none part of this contract, and the first carries a managed
			// block that trips conform-to-sdlc's own agents-managed-block rule,
			// breaking reportInitResult's "one call produces a passing
			// repo" claim the moment bd init is allowed to finish (cfm-d4r).
			Why:      "bd needs a store before " + bdConfigFile + " has a live counterpart",
			Argv:     []string{"bd", "init", "--prefix", spec.Prefix, "--non-interactive", "--skip-agents"},
			Optional: true,
		},
		{
			// checkHooksPath's repair, verbatim.
			Why:  RuleHooksPath + ": git must read hooks from the tracked " + hooksDir,
			Argv: []string{"git", "config", "core.hooksPath", hooksDir},
		},
	}
	// checkBDLive compares the live bd config against the tracked
	// declaration. Setting each key from the same bdKeys slice the renderer
	// wrote the file from is what keeps the two halves equal on day one.
	for _, k := range bdKeys {
		if bdInitOwnedKeys[k.key] {
			// bd refuses these through `config set` — they are established
			// at init and changed only by their own verb (rename-prefix).
			continue
		}
		steps = append(steps, Step{
			Why:      RuleBDConfigLive + ": live bd config must match " + bdConfigFile,
			Argv:     []string{"bd", "config", "set", k.key, bdConfigValue(k.key, spec)},
			Optional: true,
		})
	}
	return append(steps, remoteSteps(full)...)
}

// defaultBranch is the branch --fleet protects.
const defaultBranch = "main"

// bdInitOwnedKeys are the declared bd keys `bd config set` rejects: they are
// established by `bd init --prefix` and changed only through their own verb
// (`bd rename-prefix`). The plan still declares them in the tracked file —
// only the live-side set step is skipped.
var bdInitOwnedKeys = map[string]bool{"issue_prefix": true}

// remoteSteps are the GitHub-side steps, each the repair of the --fleet rule
// it closes. They create state in a real account, so nothing runs them
// unless the caller asks.
func remoteSteps(full string) []Step {
	steps := make([]Step, 0, len(fleetLabels)+2)
	for _, label := range fleetLabels {
		steps = append(steps, Step{
			Why:    RuleFleetLabels + ": fleet machinery targets this label",
			Argv:   []string{"gh", "label", "create", label, "-R", full, "--color", "5319e7", "--description", "on-demand codex review"},
			Remote: true,
		})
	}
	steps = append(steps, Step{
		Why: RuleMergePolicy + ": squash-only + delete-branch-on-merge, chosen rather than GitHub's default",
		Argv: []string{"gh", "api", "-X", "PATCH", "repos/" + full,
			"-F", "allow_squash_merge=true",
			"-F", "allow_merge_commit=false",
			"-F", "allow_rebase_merge=false",
			"-F", "delete_branch_on_merge=true"},
		Remote: true,
	})
	steps = append(steps, Step{
		Why: RuleBranchProtection + ": " + defaultBranch + " must require the " + requiredCheckContext + " status",
		Argv: []string{"gh", "api", "-X", "PUT",
			"repos/" + full + "/branches/" + defaultBranch + "/protection",
			"--input", "-"},
		Remote: true,
	})
	return steps
}

// ProtectionPayload is the body the branch-protection step reads on stdin.
// It carries the fleet amendment strict:false — parallel agent PRs pay a
// re-run tax under strict:true — and the same required context --fleet
// checks for.
func ProtectionPayload() string {
	return fmt.Sprintf(
		`{"required_status_checks":{"strict":false,"contexts":[%q]},"enforce_admins":true,"required_pull_request_reviews":null,"restrictions":null}`,
		requiredCheckContext)
}

// BootstrapOpts controls how far Bootstrap goes.
type BootstrapOpts struct {
	// WithRemote executes the GitHub steps. Off by default: they create
	// state in a real account under real credentials.
	WithRemote bool
	// DryRun runs nothing and reports every step as skipped.
	DryRun bool
	// Out receives one line per step. nil means os.Stdout.
	Out *os.File
}

// Bootstrap runs the plan for spec inside dir. Local steps run; remote steps
// run only under WithRemote and are otherwise printed for the operator to
// run by hand. A step whose command is not installed is reported and
// skipped — a missing bd must not strand a scaffolded repo.
func Bootstrap(ctx context.Context, dir string, spec ScaffoldSpec, opts BootstrapOpts) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	dir, err := resolveBootstrapDir(dir)
	if err != nil {
		return err
	}

	for _, step := range BootstrapPlan(spec) {
		if err := runBootstrapStep(ctx, dir, step, out, opts); err != nil {
			return err
		}
	}
	return nil
}

// resolveBootstrapDir makes dir absolute, since every step below runs with
// it as cmd.Dir.
func resolveBootstrapDir(dir string) (string, error) {
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s: %w", ErrBootstrap, dir, err)
	}
	return abs, nil
}

// runBootstrapStep runs one step and reports its outcome to out. A failed
// Optional step is reported and survived (nil); a failed required step stops
// Bootstrap.
func runBootstrapStep(ctx context.Context, dir string, step Step, out *os.File, opts BootstrapOpts) error {
	switch {
	case opts.DryRun:
		fmt.Fprintf(out, "  · would run: %s\n", step)
		return nil
	case step.Remote && !opts.WithRemote:
		fmt.Fprintf(out, "  · skipped (remote): %s\n", step)
		return nil
	}
	if _, err := exec.LookPath(step.Argv[0]); err != nil {
		// A missing command is reported, not fatal: a scaffolded repo must
		// not be stranded half-wired because one tool isn't installed yet.
		fmt.Fprintf(out, "  ▲ %s not on PATH — run by hand: %s\n", step.Argv[0], step)
		return nil //nolint:nilerr // deliberate: see comment above
	}
	run := runStep
	if isBDInitStep(step) {
		run = runBDInitStep
	}
	if err := run(ctx, dir, step); err != nil {
		if !step.Optional {
			return fmt.Errorf("%w: %s: %w", ErrBootstrap, step, err)
		}
		fmt.Fprintf(out, "  ▲ %s\n      %v\n      run it by hand once the cause is cleared\n", step, err)
		return nil
	}
	fmt.Fprintf(out, "  ✓ %s\n", step)
	return nil
}

// runStep executes one step in dir, feeding the protection payload on stdin
// when the step asks for it.
func runStep(ctx context.Context, dir string, step Step) error {
	ctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()
	//nolint:gosec // G204: the argv comes from BootstrapPlan, which builds it
	// from this package's own rule constants and a validated ScaffoldSpec —
	// running a named command is the point of a bootstrap step.
	cmd := exec.CommandContext(ctx, step.Argv[0], step.Argv[1:]...)
	cmd.Dir = dir
	if step.Argv[len(step.Argv)-1] == "-" {
		cmd.Stdin = strings.NewReader(ProtectionPayload())
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// isBDInitStep reports whether step is the plan's `bd init` — the one
// command that reads the tracked bd declaration before a live store exists
// (cfm-d4r).
func isBDInitStep(step Step) bool {
	return len(step.Argv) >= 2 && step.Argv[0] == "bd" && step.Argv[1] == "init"
}

// runBDInitStep runs `bd init` with the tracked sync.remote declaration held
// back, then restores the tracked file byte-identical.
//
// bd init reads .beads/config.yaml before it has a store of its own; a
// freshly scaffolded repo declares a sync.remote naming a GitHub repo that
// need not exist yet (that only happens under --with-remote, if ever), so bd
// tries to clone it and exits 1 — no live store is created at all (cfm-d4r).
// Holding the one line back for the one command that cares, then restoring
// the file exactly afterward, keeps Surface 1's contract (the declaration is
// always in the tracked file, unconditionally) while letting bd init
// succeed locally. A run with no tracked file yet (files-only, hand-rolled
// repo) has nothing to hold back and falls through to a plain run.
func runBDInitStep(ctx context.Context, dir string, step Step) error {
	// path is dir (already resolved to an absolute path by Bootstrap) joined
	// with the package's own bdConfigFile constant, not attacker input.
	path := filepath.Join(dir, bdConfigFile)
	info, statErr := os.Stat(path)
	original, err := os.ReadFile(path)
	if statErr != nil || err != nil {
		return runStep(ctx, dir, step)
	}
	mode := info.Mode().Perm()

	if err := os.WriteFile(path, stripSyncRemote(original), mode); err != nil {
		return fmt.Errorf("%w: hold back sync.remote before bd init: %w", ErrBootstrap, err)
	}

	runErr := runStep(ctx, dir, step)

	//nolint:gosec // G703: path above is dir (already resolved absolute by Bootstrap) + the package's own bdConfigFile constant, not attacker input
	if restoreErr := os.WriteFile(path, original, mode); restoreErr != nil {
		if runErr != nil {
			return fmt.Errorf("%w: restore %s failed after bd init also failed: %w: %w", ErrBootstrap, bdConfigFile, restoreErr, runErr)
		}
		return fmt.Errorf("%w: restore %s after bd init: %w", ErrBootstrap, bdConfigFile, restoreErr)
	}
	return runErr
}

// stripSyncRemote returns config bytes with the flat sync.remote line
// removed — the only shape renderBDConfig ever emits. Held back only for the
// one bd init call that would otherwise try to clone it.
func stripSyncRemote(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "sync.remote:") {
			continue
		}
		kept = append(kept, line)
	}
	return []byte(strings.Join(kept, "\n"))
}
