package checks_test

import (
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/checks"
)

// mnemdReindexFixture is tool-mnemd-reindex.sh exactly as it stood in the
// sdlc plugin at commit 9d3ed4a5 (2026-09-13, live through 2026-09-16): the
// rename that broke `mnemd reindex` went undetected for days because this
// discard shape swallowed the nonzero exit (cfm-531).
const mnemdReindexFixture = `#!/usr/bin/env bash
# tool-mnemd-reindex.sh — Stop hook: run ` + "`mnemd reindex`" + ` so a note saved straight
# into the vault is recallable by the next turn (mn-hy8.2), then ` + "`mnemd adopt`" + `
# so a note saved with no id gets one and channel: obsidian (mn-hy8.3). adopt
# reads the id-less files reindex just recorded, so it runs only when reindex
# succeeded. Moved here from boot@cc-plugins, which is disabled (sd-tfjc).
#
# Contract: always exits 0, never writes stdout (a Stop hook's stdout may be
# read as context). Silent when MNEMD_NUGBASE is unset, when mnemd is not on
# PATH, when reindex or adopt fails (an older mnemd has no adopt), and when a
# 10 s timeout fires.
#
# Detached background run, not synchronous: the Stop hook returns at once, so
# a slow reindex never holds the stop (reindex is ~0.2 s at 744 nugs today).

[ -n "${MNEMD_NUGBASE:-}" ] || exit 0
command -v mnemd >/dev/null 2>&1 || exit 0

_TIMEOUT_BIN=""
if command -v timeout >/dev/null 2>&1; then
  _TIMEOUT_BIN=timeout
elif command -v gtimeout >/dev/null 2>&1; then
  _TIMEOUT_BIN=gtimeout
fi
bounded() {
  local secs=$1
  shift
  if [ -n "$_TIMEOUT_BIN" ]; then "$_TIMEOUT_BIN" "$secs" "$@"; else "$@"; fi
}

(bounded 10 mnemd reindex && bounded 10 mnemd adopt) </dev/null >/dev/null 2>&1 &
exit 0
`

// TestHookExitDiscard_MnemdReplay: replayed against tool-mnemd-reindex.sh as
// it stood on 2026-09-16 (AC bullet 4), the check reports it — the
// backgrounded, dual-null-redirected `mnemd reindex && mnemd adopt` whose
// exit status nothing ever reads.
func TestHookExitDiscard_MnemdReplay(t *testing.T) {
	t.Parallel()

	findings := checks.HookExitDiscardFindings(".githooks/tool-mnemd-reindex.sh", mnemdReindexFixture)
	if len(findings) == 0 {
		t.Fatal("mnemd's discard shape produced no finding")
	}
	var got *checks.Finding
	for i := range findings {
		if strings.HasSuffix(findings[i].File, ":31") {
			got = &findings[i]
		}
	}
	if got == nil {
		t.Fatalf("want a finding at line 31 (the discarded reindex && adopt), got %v", findings)
	}
	if got.Rule != checks.RuleHookExitDiscard {
		t.Errorf("rule = %q, want %q", got.Rule, checks.RuleHookExitDiscard)
	}
	if got.Repair == "" {
		t.Error("finding carries no repair command")
	}

	// The command -v presence checks (line 17, guarded by || exit 0) and the
	// timeout/gtimeout probes inside their `if`/`elif` conditions (lines 20,
	// 22) must not also fire — that idiom is exactly what the fleet sweep
	// narrowed 933 raw /dev/null sites down from.
	for _, f := range findings {
		if strings.HasSuffix(f.File, ":17") || strings.HasSuffix(f.File, ":20") ||
			strings.HasSuffix(f.File, ":22") {
			t.Errorf("presence-check / condition line wrongly flagged: %v", f)
		}
	}
}

func TestHookExitDiscard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hook    string
		wantMsg string
	}{
		{
			name: "bare statement discards both channels, no status check",
			hook: "#!/bin/sh\n" +
				`bd comment "$id" "verdict: pass" >/dev/null 2>&1` + "\n" +
				`echo done` + "\n",
			wantMsg: "discards both stdout and stderr",
		},
		{
			name: "condition on the same statement is not flagged",
			hook: "#!/bin/sh\n" +
				`if command -v bd >/dev/null 2>&1; then` + "\n" +
				`  bd hooks run pre-commit` + "\n" +
				`fi` + "\n",
		},
		{
			name: "status captured on the next line is not flagged",
			hook: "#!/bin/sh\n" +
				`bd comment "$id" "verdict: pass" >/dev/null 2>&1` + "\n" +
				`st=$?` + "\n" +
				`[ "$st" -eq 0 ] || echo "comment failed" >&2` + "\n",
		},
		{
			name: "redirect to a log file is not flagged",
			hook: "#!/bin/sh\n" +
				`bd comment "$id" "verdict: pass" >/tmp/bd.log 2>&1` + "\n",
		},
		{
			name: "pipeline read without pipefail is flagged",
			hook: "#!/usr/bin/env bash\n" +
				`mnemd reindex | tee /tmp/reindex.log` + "\n" +
				`if [ $? -ne 0 ]; then` + "\n" +
				`  echo "reindex failed" >&2` + "\n" +
				`fi` + "\n",
			wantMsg: "pipeline's exit status is read without set -o pipefail",
		},
		{
			name: "pipeline read with set -o pipefail is not flagged",
			hook: "#!/usr/bin/env bash\n" +
				`set -o pipefail` + "\n" +
				`mnemd reindex | tee /tmp/reindex.log` + "\n" +
				`if [ $? -ne 0 ]; then` + "\n" +
				`  echo "reindex failed" >&2` + "\n" +
				`fi` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			findings := checks.HookExitDiscardFindings(".githooks/pre-commit", tt.hook)
			if tt.wantMsg == "" {
				if len(findings) != 0 {
					t.Errorf("want clean, got %v", findings)
				}
				return
			}
			var found bool
			for _, f := range findings {
				if f.Rule == checks.RuleHookExitDiscard && strings.Contains(f.Msg, tt.wantMsg) {
					found = true
					if f.Repair == "" {
						t.Error("finding carries no repair command")
					}
					if !strings.Contains(f.File, ":") {
						t.Errorf("File %q does not name a line number", f.File)
					}
				}
			}
			if !found {
				t.Errorf("no %s finding containing %q in %v", checks.RuleHookExitDiscard, tt.wantMsg, findings)
			}
		})
	}
}

// TestHookExitDiscard_Run: wired into Surface 1, the rule fires from a real
// repo tree and stays clean when every hook repo-standard itself ships passes it
// (AC bullet 5).
func TestHookExitDiscard_Run(t *testing.T) {
	t.Parallel()

	dir := writeRepo(t, goodRepo())
	if n := rulesOf(checks.Run(dir))[checks.RuleHookExitDiscard]; n != 0 {
		t.Errorf("repo-standard's own conforming hooks tripped hook-exit-discard: %d finding(s)", n)
	}

	files := goodRepo()
	files[".githooks/pre-commit"] = "#!/bin/sh\n" +
		`bd comment "$id" "verdict: pass" >/dev/null 2>&1` + "\n"
	dir = writeRepo(t, files)
	if n := rulesOf(checks.Run(dir))[checks.RuleHookExitDiscard]; n == 0 {
		t.Error("a discarding pre-commit hook produced no hook-exit-discard finding via Run()")
	}
}
