package checks

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/dkoosis/repo-standard/internal/sandbox"
)

// checkSandboxLib compares .sandbox/lib against the canonical copy repo-standard
// embeds. Only repos that already have a sandbox are checked — a repo without
// .sandbox/ has opted out, and this rule does not opt it back in.
//
// One finding per drifted file, because the repair is per file and a single
// rolled-up "your lib is stale" tells you nothing about what moved.
func checkSandboxLib(dir string) []Finding {
	libDir := filepath.Join(dir, sandbox.LibDir)
	if _, err := os.Stat(libDir); err != nil {
		return nil
	}

	want, err := sandbox.Files()
	if err != nil {
		// An unreadable embed is repo-standard breaking, not the repo violating a
		// rule; report it against repo-standard itself rather than blaming the repo.
		return []Finding{{
			File:   sandbox.LibDir,
			Rule:   RuleSandboxLib,
			Msg:    "repo-standard cannot read its embedded sandbox library: " + err.Error(),
			Repair: "file a bug against repo-standard — the repo is not at fault",
		}}
	}

	names, err := sandbox.Names()
	if err != nil {
		return nil
	}

	var findings []Finding
	for _, name := range names {
		rel := filepath.Join(sandbox.LibDir, name)
		have, err := os.ReadFile(filepath.Join(libDir, name))
		switch {
		case err != nil:
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleSandboxLib,
				Msg:    "missing from the sandbox library",
				Repair: "go tool repo-standard sandbox sync",
			})
		case !bytes.Equal(have, want[name]):
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleSandboxLib,
				Msg:    "drifted from the canonical sandbox library repo-standard ships",
				Repair: "go tool repo-standard sandbox sync (repo-specific env belongs in .sandbox/local-activate.sh)",
			})
		}
	}
	return findings
}
