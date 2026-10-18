package checks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Surface 2 (--local): machine wiring CI can't see. A fresh clone passes
// Surface 1 with every gate dead — hooksPath unset, bd unhydrated. This
// surface checks the live half: run it from `make doctor` and at session
// start, on the developer's machine.
const (
	// RuleHooksPath — git reads hooks from the one global hooks dir, and this
	// repo sets no local core.hooksPath (global forwarding, decision
	// 51900b14be55).
	RuleHooksPath = "hooks-path"
	// RuleBDHooks — every tracked hook is executable and delegates to bd's
	// machinery (the managed BEADS INTEGRATION block).
	RuleBDHooks = "bd-hooks"
	// RuleReviewGate — the pre-push chain is alive end to end: the global
	// pre-push forwards to the tracked .githooks/pre-push, which is
	// executable and delegates. (Which gate implementation it
	// chains is cfm-1e1.6's consolidation, not checked here.)
	RuleReviewGate = "review-gate"
	// RuleDoltRemote — bd's LIVE sync.remote is configured, so bead history
	// has an off-machine path.
	RuleDoltRemote = "dolt-remote"
	// RuleBDConfigLive — bd's live config matches the tracked declaration
	// (.beads/config.yaml) that Surface 1 checks. Two homes exist by
	// necessity (the live store is per-machine); this rule is what keeps
	// them from drifting.
	RuleBDConfigLive = "bd-config-live"
	// RuleNoGitOps is exception-only vocabulary: a repo that declares
	// {"rule": "no-git-ops"} (loto — confirmed deliberate) excepts the whole
	// git-hook family in one word. No check emits this id.
	RuleNoGitOps = "no-git-ops"
)

// noGitOpsFamily is what a no-git-ops exception expands to.
var noGitOpsFamily = []string{RuleHooksShape, RuleHookExitDiscard, RuleHooksPath, RuleBDHooks, RuleReviewGate}

// trackedHookEvents are the bd hook events a repo tracks in .githooks.
var trackedHookEvents = []string{"post-checkout", "post-merge", "pre-commit", "pre-push", "prepare-commit-msg"}

// Two delegation shapes are accepted (cfm-wml): the inline beads-managed block,
// or a delegating wrapper that exec-chains to bd's shim in .beads/hooks/.
// Both are checked semantically (marker/path presence), not byte-wise.
const (
	delegationMarker = "BEADS INTEGRATION"
	delegationShim   = ".beads/hooks"
)

// delegatesToBD reports whether hook content hands the event to bd by either
// accepted shape.
func delegatesToBD(data []byte) bool {
	return bytes.Contains(data, []byte(delegationMarker)) ||
		bytes.Contains(data, []byte(delegationShim))
}

const (
	gitTimeout = 2 * time.Second
	bdTimeout  = 2 * time.Second
)

// RunLocal executes all Surface-2 checks against the repo rooted at dir,
// with the repo's declared exceptions filtered out (a no-git-ops exception
// covers the whole hook family).
func RunLocal(ctx context.Context, dir string) []Finding {
	vals, findings := loadValues(dir)

	findings = append(findings, checkHooksPath(ctx, dir)...)
	findings = append(findings, checkBDHooks(dir)...)
	findings = append(findings, checkReviewGate(ctx, dir)...)
	findings = append(findings, checkBDLive(ctx, dir)...)

	findings = applyExceptions(findings, vals)
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings
}

// globalHooksRepair is the repair when the global hooks dir is not in effect:
// the fleet's hooks live once, there, and each forwards to the repo's
// .githooks.
const globalHooksRepair = "install the fleet's global hooks (install-hooks), which sets git config --global core.hooksPath ~/.config/git/hooks"

// forwardMarker is the call each global hook makes, once its own check has
// passed, to run <repo>/.githooks/<hook> (lib/forward-to-repo-hook.sh in the
// global hooks dir). A global hook without it never reaches the repo's hook.
const forwardMarker = "forward_to_repo_hook"

// hooksPathSetting resolves core.hooksPath as git sees it (all config scopes,
// ~ expanded) and the scope that set it. Unset is ("", "", err).
func hooksPathSetting(ctx context.Context, dir string) (scope, val string, err error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--show-scope", "--type=path", "--get", "core.hooksPath")
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err = cmd.Run()
	scope, val, _ = strings.Cut(strings.TrimSpace(stdout.String()), "\t")
	return scope, val, err
}

// localScope reports whether a config scope belongs to this repo rather than
// the machine.
func localScope(scope string) bool {
	return scope == "local" || scope == "worktree"
}

// hooksPathFinding returns the finding when the global hooks dir is not what
// git reads in dir: core.hooksPath set by the repo, or set nowhere. Otherwise
// it returns nil and the global dir git reads.
func hooksPathFinding(ctx context.Context, dir, file, rule string) (*Finding, string) {
	scope, val, err := hooksPathSetting(ctx, dir)
	switch {
	case err != nil || val == "":
		return &Finding{
			File:   file,
			Rule:   rule,
			Msg:    "core.hooksPath unset — git reads .git/hooks, so neither the fleet's global gates nor this repo's .githooks run",
			Repair: globalHooksRepair,
		}, ""
	case localScope(scope):
		return &Finding{
			File:   file,
			Rule:   rule,
			Msg:    fmt.Sprintf("this repo sets core.hooksPath to %q (%s config) — a local hooks path shadows the fleet's global gates; no repo sets one", val, scope),
			Repair: fmt.Sprintf("git config --%s --unset core.hooksPath", scope),
		}, ""
	}
	return nil, val
}

// checkHooksPath verifies global forwarding is live (hooks-path): git reads
// hooks from the global hooks dir, and this repo does not shadow it with a
// local core.hooksPath.
func checkHooksPath(ctx context.Context, dir string) []Finding {
	if f, _ := hooksPathFinding(ctx, dir, ".git/config", RuleHooksPath); f != nil {
		return []Finding{*f}
	}
	return nil
}

// checkBDHooks verifies every tracked hook event is present, executable, and
// delegates to bd (bd-hooks).
func checkBDHooks(dir string) []Finding {
	var findings []Finding
	for _, event := range trackedHookEvents {
		rel := filepath.Join(hooksDir, event)
		path := filepath.Join(dir, rel)
		fi, err := os.Stat(path)
		if err != nil {
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleBDHooks,
				Msg:    event + " hook missing — bd's " + event + " machinery never runs on this machine",
				Repair: "bd hooks install (then commit the .githooks sources)",
			})
			continue
		}
		if fi.Mode()&0o111 == 0 {
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleBDHooks,
				Msg:    "hook is not executable — git skips it without a word",
				Repair: "chmod +x " + rel,
			})
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || !delegatesToBD(data) {
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleBDHooks,
				Msg:    "hook neither carries the beads-managed block nor exec-chains to the .beads/hooks shim — bd events silently skipped",
				Repair: "bd hooks install (inline block), or wrap: exec \"$repo_root/.beads/hooks/$hook_name\"",
			})
		}
	}
	return findings
}

// checkReviewGate verifies the pre-push chain end to end (review-gate): the
// global hooks dir in effect, its pre-push forwarding to the repo's, and the
// repo's pre-push executable and delegating. Any dead link means the gate
// reads as protection and provides none.
func checkReviewGate(ctx context.Context, dir string) []Finding {
	rel := filepath.Join(hooksDir, "pre-push")

	f, global := hooksPathFinding(ctx, dir, rel, RuleReviewGate)
	if f != nil {
		f.Msg = "pre-push gate unreachable — " + f.Msg
		return []Finding{*f}
	}

	fi, statErr := os.Stat(filepath.Join(dir, rel))
	if statErr != nil || fi.Mode()&0o111 == 0 {
		return []Finding{{
			File:   rel,
			Rule:   RuleReviewGate,
			Msg:    "pre-push missing or not executable — nothing gates a push from this machine",
			Repair: "bd hooks install && chmod +x " + rel,
		}}
	}

	globalPush := filepath.Join(global, "pre-push")
	gfi, gStatErr := os.Stat(globalPush)
	gdata, gReadErr := os.ReadFile(globalPush)
	if gStatErr != nil || gfi.Mode()&0o111 == 0 || gReadErr != nil || !bytes.Contains(gdata, []byte(forwardMarker)) {
		return []Finding{{
			File:   rel,
			Rule:   RuleReviewGate,
			Msg:    "global pre-push (" + globalPush + ") is missing, not executable, or does not forward to " + rel + " — the repo's push gate never runs",
			Repair: globalHooksRepair,
		}}
	}

	data, readErr := os.ReadFile(filepath.Join(dir, rel))
	if readErr != nil || !delegatesToBD(data) {
		return []Finding{{
			File:   rel,
			Rule:   RuleReviewGate,
			Msg:    "pre-push does not delegate to bd — the push gate chain is broken at the last link",
			Repair: "bd hooks install (re-emits the managed delegation block)",
		}}
	}
	return nil
}

// checkBDLive verifies bd's live config: sync.remote configured
// (dolt-remote) and all three keys matching the tracked declaration
// (bd-config-live). One `bd config list` exec — parallel bd invocations
// contend on the embedded Dolt lock (cfm-1e1.2 dead end).
func checkBDLive(ctx context.Context, dir string) []Finding {
	if _, err := exec.LookPath("bd"); err != nil {
		return []Finding{{
			File:   ".beads",
			Rule:   RuleBDConfigLive,
			Msg:    "bd not on PATH — live config unverifiable on this machine",
			Repair: "install beads (bd), then bd init/hydrate this repo",
		}}
	}

	live, err := bdConfigList(ctx, dir)
	if err != nil {
		return []Finding{{
			File:   ".beads",
			Rule:   RuleBDConfigLive,
			Msg:    fmt.Sprintf("bd config list failed (%v) — no live beads store on this machine", err),
			Repair: "bd init --prefix <p> (or hydrate), then bd config set the declared keys",
		}}
	}

	var findings []Finding
	if live["sync.remote"] == "" {
		findings = append(findings, Finding{
			File:   ".beads",
			Rule:   RuleDoltRemote,
			Msg:    "live sync.remote unset — bead history on this machine has no off-machine path",
			Repair: "bd config set sync.remote <declared value from " + bdConfigFile + ">",
		})
	}

	declared := declaredBDConfig(dir)
	for _, k := range bdKeys {
		want := declared[k.key]
		if want == "" {
			continue // undeclared is Surface 1's finding, not a live mismatch
		}
		if got := live[k.key]; got != want {
			findings = append(findings, Finding{
				File:   ".beads",
				Rule:   RuleBDConfigLive,
				Msg:    fmt.Sprintf("live %s is %q, declaration says %q — the two homes have drifted", k.key, got, want),
				Repair: fmt.Sprintf("bd config set %s %q (or fix %s if the live value is right)", k.key, want, bdConfigFile),
			})
		}
	}
	return findings
}

// bdConfigList runs one `bd config list` and parses its "key = value" lines.
func bdConfigList(ctx context.Context, dir string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, bdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bd", "config", "list")
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	values := make(map[string]string)
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return values, nil
}
