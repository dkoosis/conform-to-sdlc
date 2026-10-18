package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Fleet git-hook shape is global forwarding (decision 51900b14be55, ratified
// 2026-09-18, superseding c9ab7db0d7ac): fleet-wide gates live once, in the
// global core.hooksPath (~/.config/git/hooks), and each global hook forwards
// to <repo>/.githooks/<hook> when present. A repo's own hooks are TRACKED
// source files in .githooks delegating to bd's machinery; .beads/hooks
// (tool-generated, usually gitignored, invisible to review, empty on a fresh
// clone) is not a hook home. No repo sets a local core.hooksPath.
//
// Surface 1 checks the repo-file half: .githooks exists with executable
// pre-commit and pre-push (the gate carriers). Whether the global hooks dir
// is in effect and forwards to them is machine state — that half belongs to
// --local.
const hooksDir = ".githooks"

var requiredHooks = []string{"pre-commit", "pre-push"}

// checkHooksShape verifies the tracked .githooks directory (hooks-shape).
func checkHooksShape(dir string) []Finding {
	info, err := os.Stat(filepath.Join(dir, hooksDir))
	if err != nil || !info.IsDir() {
		return []Finding{{
			File:   hooksDir,
			Rule:   RuleHooksShape,
			Msg:    "no tracked .githooks directory — the global hooks forward to .githooks, the one home for a repo's hooks; untracked hooks are invisible to review and empty on a fresh clone",
			Repair: "track hook sources in .githooks/ (not .beads/hooks); set no local core.hooksPath",
		}}
	}

	var findings []Finding
	for _, h := range requiredHooks {
		rel := filepath.Join(hooksDir, h)
		fi, err := os.Stat(filepath.Join(dir, rel))
		switch {
		case err != nil:
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleHooksShape,
				Msg:    h + " hook missing — the gate it carries silently never fires",
				Repair: fmt.Sprintf("add %s delegating to bd's hook machinery", rel),
			})
		case fi.Mode()&0o111 == 0:
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleHooksShape,
				Msg:    "hook is not executable — git skips it without a word",
				Repair: "chmod +x " + rel,
			})
		}
	}
	return findings
}

// A hook may choose to continue past a failure, but the discard must be
// visible to a person or a log — not silent (cfm-531). mnemd's
// tool-mnemd-reindex.sh discarded a renamed verb's failure for four days;
// sdlc's gate-task-close.sh (pre-e8b910c) reported a verdict it never
// checked was written. A fleet-wide sweep found 933 raw >/dev/null sites but
// only ~29 bare statements invoking a tool outside a condition or the
// `command -v` presence-check idiom, and of those only one was a real bug —
// so these patterns match the bare-statement shape, not any redirect to
// /dev/null.
var (
	// hookNullBothRe: one statement sends BOTH stdout and stderr to
	// /dev/null — the loudest "we don't want to know" shape.
	hookNullBothRe = regexp.MustCompile(`>\s*/dev/null\s+2>&1|&>\s*/dev/null|>&\s*/dev/null`)

	// hookConditionHeadRe / hookConditionTailRe: the statement is a
	// condition (if/elif/while/until), so its exit status IS read — by the
	// control-flow keyword guarding it, not discarded.
	hookConditionHeadRe = regexp.MustCompile(`^\s*(if|elif|while|until)\s+`)
	hookConditionTailRe = regexp.MustCompile(`;\s*(then|do)\s*$`)

	// hookPresenceCheckRe: `command -v`/`command -V` tests whether a binary
	// exists; throwing its output away is the point, not a bug.
	hookPresenceCheckRe = regexp.MustCompile(`\bcommand\s+-[vV]\b`)

	// hookStatusRe: `$?` read on this line or the next — the status was
	// captured or tested, even though the tool's own output was discarded.
	hookStatusRe = regexp.MustCompile(`\$\?`)

	// hookBackgroundedRe: a bare statement backgrounded with a single
	// trailing `&` (not `&&`) — its exit status is gone unless something
	// later waits on it.
	hookBackgroundedRe = regexp.MustCompile(`[^&]&\s*$`)

	// hookPipeRe: an unquoted pipe joining two commands (not `||`).
	hookPipeRe = regexp.MustCompile(`[^|]\|[^|]`)
)

// checkHookExitDiscard scans every file in .githooks for a hook invocation
// whose failure is invisible: output silenced on both channels, a
// backgrounded job nothing waits on, or a piped exit status read without
// set -o pipefail / PIPESTATUS (hook-exit-discard).
func checkHookExitDiscard(dir string) []Finding {
	entries, err := os.ReadDir(filepath.Join(dir, hooksDir))
	if err != nil {
		return nil // a missing .githooks is hooks-shape's finding, not this rule's
	}

	var findings []Finding
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		rel := filepath.Join(hooksDir, e.Name())
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		findings = append(findings, hookExitDiscardFindings(rel, string(data))...)
	}
	return findings
}

// hookScanCtx holds the file-wide facts a per-line check needs — computed
// once so each shape's detector stays a simple predicate.
type hookScanCtx struct {
	rel        string
	lines      []string
	noSetE     bool
	hasWait    bool
	noPipefail bool
}

// hookExitDiscardFindings is the line-level detector, split out so a test can
// replay a fixture script's exact content without materializing a repo.
func hookExitDiscardFindings(rel, content string) []Finding {
	ctx := hookScanCtx{
		rel:        rel,
		lines:      strings.Split(content, "\n"),
		noSetE:     !strings.Contains(content, "set -e"),
		hasWait:    strings.Contains(content, "wait"),
		noPipefail: !strings.Contains(content, "pipefail") && !strings.Contains(content, "PIPESTATUS"),
	}

	var findings []Finding
	for i, line := range ctx.lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if f := ctx.lineFinding(i, line, trimmed); f != nil {
			findings = append(findings, *f)
		}
	}
	return findings
}

// lineFinding checks one non-blank, non-comment line against every shape and
// returns the first that fires — a line earns at most one finding.
func (ctx hookScanCtx) lineFinding(i int, line, trimmed string) *Finding {
	lineNo := i + 1
	guarded := ctx.isGuarded(i, line, trimmed)

	switch {
	case hookNullBothRe.MatchString(line) && !guarded:
		return &Finding{
			File:   fmt.Sprintf("%s:%d", ctx.rel, lineNo),
			Rule:   RuleHookExitDiscard,
			Msg:    "tool invocation discards both stdout and stderr with nothing checking its exit status — a failure here is invisible to a person or a log",
			Repair: "capture the status (`...; st=$?`) and log or act on a nonzero exit, or redirect to a log file instead of /dev/null",
		}
	case hookBackgroundedRe.MatchString(line) && !ctx.isConditionOrPresenceCheck(line, trimmed) && ctx.noSetE && !ctx.hasWait:
		return &Finding{
			File:   fmt.Sprintf("%s:%d", ctx.rel, lineNo),
			Rule:   RuleHookExitDiscard,
			Msg:    "backgrounded invocation's exit status is never waited on or checked, and the script has no set -e — the script returns before a failure here can matter",
			Repair: "wait \"$!\" and check $?, or drop the trailing & and let set -e propagate the failure",
		}
	case hookPipeRe.MatchString(trimmed) && ctx.noPipefail && ctx.statusReadNearby(i, trimmed):
		return &Finding{
			File:   fmt.Sprintf("%s:%d", ctx.rel, lineNo),
			Rule:   RuleHookExitDiscard,
			Msg:    "pipeline's exit status is read without set -o pipefail or PIPESTATUS — $? reflects the last stage, not the tool being checked",
			Repair: "add `set -o pipefail`, or read ${PIPESTATUS[0]} for the stage that matters",
		}
	}
	return nil
}

// isConditionOrPresenceCheck reports whether the statement's exit status is
// already read by control flow (if/elif/while/until) or the line is the
// command -v/-V presence-check idiom.
func (ctx hookScanCtx) isConditionOrPresenceCheck(line, trimmed string) bool {
	isCondition := hookConditionHeadRe.MatchString(line) || hookConditionTailRe.MatchString(trimmed)
	return isCondition || hookPresenceCheckRe.MatchString(line)
}

// isGuarded reports whether a both-channels-to-null statement's exit status
// is nonetheless visible: it is a condition or presence check, or the status
// is handled via a trailing || or a $? read on this or the next line.
func (ctx hookScanCtx) isGuarded(i int, line, trimmed string) bool {
	if ctx.isConditionOrPresenceCheck(line, trimmed) {
		return true
	}
	next := ctx.nextNonBlankLine(i)
	return strings.Contains(line, "||") || hookStatusRe.MatchString(line) || hookStatusRe.MatchString(next)
}

// statusReadNearby reports whether $? is read on the pipeline's own line or
// the next non-blank line — the shape that misreads a pipeline's status.
func (ctx hookScanCtx) statusReadNearby(i int, trimmed string) bool {
	return hookStatusRe.MatchString(trimmed) || hookStatusRe.MatchString(ctx.nextNonBlankLine(i))
}

// nextNonBlankLine returns the next non-empty line after index i, or "".
func (ctx hookScanCtx) nextNonBlankLine(i int) string {
	for j := i + 1; j < len(ctx.lines); j++ {
		if s := strings.TrimSpace(ctx.lines[j]); s != "" {
			return s
		}
	}
	return ""
}
