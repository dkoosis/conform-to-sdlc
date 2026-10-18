package checks_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// delegatingHook carries the beads-managed block marker the bd-hooks and
// review-gate rules look for.
const delegatingHook = `#!/bin/sh
# --- BEGIN BEADS INTEGRATION v1.0.5 ---
bd hooks run "$(basename "$0")" "$@"
# --- END BEADS INTEGRATION v1.0.5 ---
`

var hookEvents = []string{"post-checkout", "post-merge", "pre-commit", "pre-push", "prepare-commit-msg"}

// wrapperHook is an alternate shape (cfm-wml): a tracked wrapper that
// exec-chains to bd's shim instead of carrying the inline managed block.
const wrapperHook = `#!/usr/bin/env sh
hook_name="$(basename "$0")"
repo_root="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
shim="$repo_root/.beads/hooks/$hook_name"
if [ -x "$shim" ]; then
  exec "$shim" "$@"
fi
exit 0
`

// localRepo returns a full tracked .githooks set plus the Surface-1 files the
// values loader needs.
func localRepo() map[string]string {
	files := map[string]string{
		"docs/conform.json":  goodValues,
		".beads/config.yaml": goodBDConfig,
	}
	for _, e := range hookEvents {
		files[filepath.Join(".githooks", e)] = delegatingHook
	}
	return files
}

// initGit makes dir a git repo, hermetic from global/system config, and sets
// a local core.hooksPath when non-empty. Uses t.Setenv, so callers must not
// be parallel; call globalHooks after it.
func initGit(t *testing.T, dir, hooksPath string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if hooksPath != "" {
		run("config", "core.hooksPath", hooksPath)
	}
}

// globalHooks points git's global config at a fresh global hooks dir holding
// an executable pre-push, which forwards to the repo's .githooks/pre-push when
// forwards is true, and returns the dir. Uses t.Setenv — not parallel.
func globalHooks(t *testing.T, forwards bool) string {
	t.Helper()
	hooks := t.TempDir()
	body := "#!/bin/sh\nexit 0\n"
	if forwards {
		body = "#!/bin/sh\n. \"$(dirname \"$0\")/lib/forward-to-repo-hook.sh\"\nforward_to_repo_hook pre-push \"\" \"$@\"\n"
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[core]\n\thooksPath = "+hooks+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	return hooks
}

// fakeBD prepends a stub bd to PATH answering `bd config list` with the
// given values ("" leaves a key unset). Uses t.Setenv — not parallel.
func fakeBD(t *testing.T, planDir, prefix, remote string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
echo "Configuration:"
echo "  custom.plan_dir = ` + planDir + `"
echo "  issue_prefix = ` + prefix + `"
echo "  sync.remote = ` + remote + `"
`
	if err := os.WriteFile(filepath.Join(bin, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestRunLocal_ConformingMachine: global hooks dir forwarding, no local
// hooksPath, full delegating hook set, live bd config matching the
// declaration = zero findings.
func TestRunLocal_ConformingMachine(t *testing.T) {
	dir := writeRepo(t, localRepo())
	initGit(t, dir, "")
	globalHooks(t, true)
	fakeBD(t, "/vault/plans", "cfm", "git+https://github.com/x/y.git")

	if findings := checks.RunLocal(context.Background(), dir); len(findings) != 0 {
		t.Fatalf("conforming machine produced findings:\n%v", findings)
	}
}

// TestRunLocal_DeadWiring: a machine with core.hooksPath unset everywhere
// trips hooks-path and review-gate.
func TestRunLocal_DeadWiring(t *testing.T) {
	dir := writeRepo(t, localRepo())
	initGit(t, dir, "")
	fakeBD(t, "/vault/plans", "cfm", "git+https://github.com/x/y.git")

	rules := rulesOf(checks.RunLocal(context.Background(), dir))
	for _, want := range []string{checks.RuleHooksPath, checks.RuleReviewGate} {
		if rules[want] == 0 {
			t.Errorf("dead wiring produced no %s finding (got %v)", want, rules)
		}
	}
}

// TestRunLocal_NoGitOpsException: loto's declared exception silences the
// whole hook family in one word; the bd rules still run.
func TestRunLocal_NoGitOpsException(t *testing.T) {
	files := map[string]string{
		"docs/conform.json": `{"profile": "tool", "exceptions": [
			{"rule": "no-git-ops", "reason": "loto never performs git operations by design"}]}`,
		".beads/config.yaml": goodBDConfig,
	}
	dir := writeRepo(t, files) // no hooks at all
	initGit(t, dir, "")
	fakeBD(t, "/vault/plans", "cfm", "") // live sync.remote unset

	rules := rulesOf(checks.RunLocal(context.Background(), dir))
	for _, silenced := range []string{checks.RuleHooksPath, checks.RuleBDHooks, checks.RuleReviewGate} {
		if rules[silenced] != 0 {
			t.Errorf("no-git-ops should silence %s, got %v", silenced, rules)
		}
	}
	if rules[checks.RuleDoltRemote] == 0 {
		t.Errorf("no-git-ops must not silence dolt-remote, got %v", rules)
	}
}

// TestHooksPath_Violations: global forwarding (decision 51900b14be55) — the
// global hooks dir passes; unset, or any hooksPath the repo sets itself,
// is a finding.
func TestHooksPath_Violations(t *testing.T) {
	tests := []struct {
		name      string
		localPath string
		global    bool
		wantMsg   string
	}{
		{name: "global hooks dir", global: true},
		{name: "unset", wantMsg: "core.hooksPath unset"},
		{name: "local .githooks", localPath: ".githooks", global: true, wantMsg: "shadows the fleet's global gates"},
		{name: "local .beads/hooks", localPath: ".beads/hooks", global: true, wantMsg: "shadows the fleet's global gates"},
		{name: "local .githooks, no global", localPath: ".githooks", wantMsg: "shadows the fleet's global gates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRepo(t, nil)
			initGit(t, dir, tt.localPath)
			if tt.global {
				globalHooks(t, true)
			}
			findings := checks.CheckHooksPathFn(context.Background(), dir)
			assertOneOrClean(t, findings, checks.RuleHooksPath, tt.wantMsg)
		})
	}
}

// A local hooksPath naming the global dir itself is still a repo setting
// one: the decision bars any local core.hooksPath.
func TestHooksPath_LocalCopyOfGlobalDir(t *testing.T) {
	dir := writeRepo(t, nil)
	initGit(t, dir, "")
	hooks := globalHooks(t, true)
	cmd := exec.Command("git", "config", "core.hooksPath", hooks)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}
	assertOneOrClean(t, checks.CheckHooksPathFn(context.Background(), dir), checks.RuleHooksPath, "local config")
}

func TestBDHooks_Violations(t *testing.T) {
	t.Parallel()

	t.Run("full delegating set is clean", func(t *testing.T) {
		t.Parallel()
		dir := writeRepo(t, localRepo())
		assertOneOrClean(t, checks.CheckBDHooks(dir), checks.RuleBDHooks, "")
	})

	t.Run("missing event", func(t *testing.T) {
		t.Parallel()
		files := localRepo()
		delete(files, ".githooks/post-merge")
		dir := writeRepo(t, files)
		assertOneOrClean(t, checks.CheckBDHooks(dir), checks.RuleBDHooks, "post-merge hook missing")
	})

	t.Run("delegating wrapper shape accepted", func(t *testing.T) {
		t.Parallel()
		files := localRepo()
		for _, e := range hookEvents {
			files[".githooks/"+e] = wrapperHook
		}
		dir := writeRepo(t, files)
		assertOneOrClean(t, checks.CheckBDHooks(dir), checks.RuleBDHooks, "")
	})

	t.Run("no delegation block", func(t *testing.T) {
		t.Parallel()
		files := localRepo()
		files[".githooks/pre-commit"] = "#!/bin/sh\nexit 0\n"
		dir := writeRepo(t, files)
		assertOneOrClean(t, checks.CheckBDHooks(dir), checks.RuleBDHooks, "neither carries")
	})
}

func TestReviewGate_BrokenLinks(t *testing.T) {
	t.Run("clean chain", func(t *testing.T) {
		dir := writeRepo(t, localRepo())
		initGit(t, dir, "")
		globalHooks(t, true)
		assertOneOrClean(t, checks.CheckReviewGate(context.Background(), dir), checks.RuleReviewGate, "")
	})

	t.Run("local hooksPath shadows the global pre-push", func(t *testing.T) {
		dir := writeRepo(t, localRepo())
		initGit(t, dir, ".githooks")
		globalHooks(t, true)
		assertOneOrClean(t, checks.CheckReviewGate(context.Background(), dir), checks.RuleReviewGate, "unreachable")
	})

	t.Run("global pre-push does not forward", func(t *testing.T) {
		dir := writeRepo(t, localRepo())
		initGit(t, dir, "")
		globalHooks(t, false)
		assertOneOrClean(t, checks.CheckReviewGate(context.Background(), dir), checks.RuleReviewGate, "does not forward")
	})

	t.Run("global pre-push missing", func(t *testing.T) {
		dir := writeRepo(t, localRepo())
		initGit(t, dir, "")
		hooks := globalHooks(t, true)
		if err := os.Remove(filepath.Join(hooks, "pre-push")); err != nil {
			t.Fatal(err)
		}
		assertOneOrClean(t, checks.CheckReviewGate(context.Background(), dir), checks.RuleReviewGate, "does not forward")
	})

	t.Run("pre-push not executable", func(t *testing.T) {
		dir := writeRepo(t, localRepo())
		initGit(t, dir, "")
		globalHooks(t, true)
		if err := os.Chmod(filepath.Join(dir, ".githooks", "pre-push"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertOneOrClean(t, checks.CheckReviewGate(context.Background(), dir), checks.RuleReviewGate, "missing or not executable")
	})

	t.Run("no delegation", func(t *testing.T) {
		files := localRepo()
		files[".githooks/pre-push"] = "#!/bin/sh\nexit 0\n"
		dir := writeRepo(t, files)
		initGit(t, dir, "")
		globalHooks(t, true)
		assertOneOrClean(t, checks.CheckReviewGate(context.Background(), dir), checks.RuleReviewGate, "does not delegate")
	})
}

func TestBDLive(t *testing.T) {
	dir := writeRepo(t, map[string]string{".beads/config.yaml": goodBDConfig})

	t.Run("live matches declaration", func(t *testing.T) {
		fakeBD(t, "/vault/plans", "cfm", "git+https://github.com/x/y.git")
		findings := checks.CheckBDLive(context.Background(), dir)
		if len(findings) != 0 {
			t.Errorf("matching live config produced findings: %v", findings)
		}
	})

	t.Run("live sync.remote unset", func(t *testing.T) {
		fakeBD(t, "/vault/plans", "cfm", "")
		findings := checks.CheckBDLive(context.Background(), dir)
		assertOneOrClean(t, findings, checks.RuleDoltRemote, "no off-machine path")
	})

	t.Run("live drifted from declaration", func(t *testing.T) {
		fakeBD(t, "/elsewhere/plans", "cfm", "git+https://github.com/x/y.git")
		findings := checks.CheckBDLive(context.Background(), dir)
		assertOneOrClean(t, findings, checks.RuleBDConfigLive, "have drifted")
	})

	t.Run("bd not on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		findings := checks.CheckBDLive(context.Background(), dir)
		assertOneOrClean(t, findings, checks.RuleBDConfigLive, "bd not on PATH")
	})
}
