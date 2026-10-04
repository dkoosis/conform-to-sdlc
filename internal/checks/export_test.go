package checks

import "context"

// Test hooks: the per-area checks stay unexported in the API; tests reach
// them directly so unit cases don't drag the whole runner (and its bd exec)
// along.
var (
	CheckMakefile     = checkMakefile
	CheckLintFloor    = checkLintFloor
	CheckLintPin      = checkLintPin
	CheckCIGate       = checkCIGate
	CheckCIDocsSkip   = checkCIDocsSkip
	CheckCodexShape   = checkCodexShape
	CheckRetiredFiles = checkRetiredFiles
	CheckBDConfig     = checkBDConfig
	CheckHooksShape   = checkHooksShape
	CheckRootMinimal  = checkRootMinimal
)

// CheckHookExitDiscard is the hook-exit-discard rule's directory-scanning
// half (cfm-531); HookExitDiscardFindings is the line-level detector a test
// can replay a fixture script's exact content through.
var (
	CheckHookExitDiscard    = checkHookExitDiscard
	HookExitDiscardFindings = hookExitDiscardFindings
)

// Surface-2 test hooks.
var (
	CheckHooksPathFn = checkHooksPath
	CheckBDHooks     = checkBDHooks
	CheckReviewGate  = checkReviewGate
	CheckBDLive      = checkBDLive
)

// SetGHAPI swaps the gh fetcher for tests; the returned func restores it.
func SetGHAPI(f func(ctx context.Context, path string) ([]byte, error)) func() {
	old := ghAPI
	ghAPI = f
	return func() { ghAPI = old }
}

// ErrNotFound lets fleet tests simulate 404s.
var ErrNotFound = errNotFound

// v0.2.0 test hooks.

// CheckReadme is the readme rule's verify half (sd-mzgy.5).
var CheckReadme = checkReadme

// CheckAgentsStub is the agents-stub rule (sd-9uw2).
var CheckAgentsStub = checkAgentsStub

// AgentsLineCap exposes the body ceiling so the test states the same number
// the check enforces rather than a copy that can drift.
const AgentsLineCap = agentsLineCap

// RootStrayNames lists the files root-minimal flags, for the per-file test.
func RootStrayNames() []string {
	names := make([]string, 0, len(rootStrays))
	for _, s := range rootStrays {
		names = append(names, s.name)
	}
	return names
}
