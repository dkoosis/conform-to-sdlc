// Package values defines the per-repo values file schema and its loader —
// the escape valve for whatever the reference repo (ferret) can't state
// generically for the whole fleet. Whatever ferret can't state generically
// becomes a per-repo value, and every value carries a reason.
//
// Every dkoosis fleet repo declares a docs/repo-standard.json:
//
//	{
//	  "profile": "tool",
//	  "exceptions": [
//	    {"rule": "no-git-ops", "reason": "loto never performs git operations by design; declared exception, never fix"}
//	  ]
//	}
//
// Profile is one of two values: tool (the full SDLC surface) or lib (the
// same contract minus the deploy verb; beads optional). lib is profile
// semantics, not an exception — a lib repo declares no "no-deploy"
// exception; it simply carries a different profile. See
// testdata/atomicfile.json.
//
// Every exception names a rule and a non-empty reason; an unreasoned
// exception is a hard error, never a warning. An exception appearing twice
// across the fleet is a signal it belongs in a profile instead of scattered
// per-repo (a design call this package does not enforce).
//
// Loading is strict about unknown fields and trailing data: unknown fields
// at any level (top-level or inside an exception) are hard errors via
// json.Decoder's DisallowUnknownFields, trailing data after the top-level
// object is a hard error, and every failure is wrapped with the file path
// by Load. (Duplicate JSON keys still resolve last-wins — a stdlib
// encoding/json limitation; this package does not add token-level detection
// for it.) There is no soft-fail or warn path anywhere in this package.
//
// # Boundary with project.conf
//
// go-sandbox's project.conf stays untouched and shell-sourceable. Facts it
// already owns — GOLANGCI_LINT_VERSION, BUILD_SYSTEM, tool lists — are
// explicitly OUT of this schema. Machine-sourceable shell config lives in
// project.conf; structured per-repo policy (profile plus reasoned
// exceptions) lives in repo-standard.json. One home per fact: nothing here ever
// duplicates a project.conf key, and repo-standard.json is never made
// shell-sourceable to blur that line.
//
// # Fleet roster
//
// fleet.json is the one central roster, go:embed'ed so `repo-standard --fleet`
// runs from the installed binary without a checkout of every repo. It owns
// fleet-level facts only — a repo's name and its GitHub visibility (the
// public/private split) — never profile or exceptions, which stay per-repo.
//
// cc-plugins is deliberately absent from the roster: it is markdown-only,
// outside the Go contract repo-standard enforces. The roster is the authority on
// fleet membership, so the omission is the decision, not an oversight.
package values
