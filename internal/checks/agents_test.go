package checks_test

import (
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// ownAgents is a repo's own instructions, in the shape sdlc keeps its own:
// content, not a pointer, and longer than any cap this rule once had.
var ownAgents = "# repo\n\n" + strings.Repeat("- Least code that does the job.\n", 40)

// lardedAgents is canapay's shape, measured 2026-09-08: the repo's words with
// a bd-injected managed block bolted underneath.
const lardedAgents = `# repo

- Least code that does the job.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal -->
## Task tracking

This repo uses bd. Run ` + "`bd ready`" + ` to see what is unblocked.
<!-- END BEADS INTEGRATION -->
`

// TestAgentsBlock_OwnWordsPass: an AGENTS.md that holds the repo's
// instructions, at any length, produces nothing.
func TestAgentsBlock_OwnWordsPass(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[checks.AgentsFile] = ownAgents
	dir := writeRepo(t, files)
	if got := checks.CheckAgentsBlock(dir); len(got) != 0 {
		t.Fatalf("AGENTS.md in the repo's own words: want no findings, got %+v", got)
	}
}

// TestAgentsBlock_AbsentIsNotAFinding: the rule guards what a tool writes
// into the file, not the file's existence.
func TestAgentsBlock_AbsentIsNotAFinding(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, goodRepo())
	if got := checks.CheckAgentsBlock(dir); len(got) != 0 {
		t.Fatalf("no AGENTS.md: want no findings, got %+v", got)
	}
}

// TestAgentsBlock_ManagedBlockIsAFinding: the case the rule exists for, with
// the repair asserted.
func TestAgentsBlock_ManagedBlockIsAFinding(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[checks.AgentsFile] = lardedAgents
	dir := writeRepo(t, files)
	got := checks.CheckAgentsBlock(dir)
	if len(got) != 1 || got[0].File != checks.AgentsFile || got[0].Rule != checks.RuleAgentsBlock {
		t.Fatalf("larded AGENTS.md: want one %s finding on %s, got %+v",
			checks.RuleAgentsBlock, checks.AgentsFile, got)
	}
	if !strings.Contains(got[0].Repair, "delete") {
		t.Fatalf("managed block repair must say delete, got %q", got[0].Repair)
	}
}

// TestRun_LardedAgentsFailsTheGate: the rule is wired into Run, not just
// reachable from a test hook. Without this the check could pass its own unit
// cases while conform-to-sdlc stayed green on a larded repo.
func TestRun_LardedAgentsFailsTheGate(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[checks.AgentsFile] = lardedAgents
	dir := writeRepo(t, files)
	var hit bool
	for _, f := range checks.Run(dir) {
		if f.Rule == checks.RuleAgentsBlock && f.File == checks.AgentsFile {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("larded AGENTS.md: want an %s finding from Run, got %+v",
			checks.RuleAgentsBlock, checks.Run(dir))
	}
}
