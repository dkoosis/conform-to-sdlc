package checks

import (
	"os"
	"path/filepath"
	"strings"
)

// AgentsFile is where a repo tells agents how to work in it. sdlc keeps its
// own instructions there, with a one-line CLAUDE.md that imports it for Claude
// Code, which reads CLAUDE.md and never AGENTS.md
// (code.claude.com/docs/en/memory.md). What the file says is the repo's
// business; this checker reads it only for a block a tool wrote.
const AgentsFile = "AGENTS.md"

// managedBlockOpen is how a tool marks a region it owns and will rewrite. bd's
// is `<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal -->`; the convention is
// shared, so the detector matches the shape rather than one vendor's spelling.
//
// ‡ Blind spot, named because a check owes one: a tool that injects without
// markers is not caught.
const managedBlockOpen = "<!-- BEGIN"

// checkAgentsBlock reports a root AGENTS.md that carries a tool-managed block
// (agents-managed-block).
//
// Absent is not a finding, and neither is length: the file is the repo's own
// words. A managed block is not — canapay's AGENTS.md was 119 lines on
// 2026-09-08, of which 66-119 were bd's.
//
// Why an enforcing rule and not a one-time cleanup: bd re-injects its block on
// some commands, so a swept repo regresses silently. A check is the only form
// of that decision that survives the next tool deciding the root file is a
// good place to write.
func checkAgentsBlock(dir string) []Finding {
	data, err := os.ReadFile(filepath.Join(dir, AgentsFile))
	if err != nil {
		return nil
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), managedBlockOpen) {
			return []Finding{{
				File:   AgentsFile,
				Rule:   RuleAgentsBlock,
				Msg:    "a tool-managed block in " + AgentsFile + " — the file holds what the repo's people wrote, and a marked block regenerates after any hand cleanup",
				Repair: "delete the block and its END marker; if a line in it is worth keeping, write it into " + AgentsFile + " outside any marker",
			}}
		}
	}
	return nil
}
