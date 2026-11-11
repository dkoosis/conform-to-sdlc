package checks

import (
	"fmt"
	"os"
	"path/filepath"
)

// retiredFiles are per-repo files whose job repo-standard absorbed or whose
// existence is a live footgun. Presence is a finding; the gate stays red
// until the file is gone (retired-files).
//
//   - check-pack-drift.sh soft-failed by design (its upstream in cc-plugins is
//     gone; the script warns and exits 0). A
//     rule that cannot fail is worse than an absent rule; lint-pack guards the
//     pack now, offline. Repos kept it under scripts/ or .golangci-rules/.
//   - scaffold-codex-review.sh emits an auto-fire pull_request trigger — a
//     re-run is surprise OpenAI spend (the deployed workflows are
//     issue_comment). The codex-workflow rule owns the shape; the scaffolder
//     must not exist where it can be re-run.
var retiredFiles = []string{
	"scripts/check-pack-drift.sh",
	".golangci-rules/check-pack-drift.sh",
	"scripts/scaffold-codex-review.sh",
}

// renamedFiles are files this tool read under its old name, conform-to-sdlc.
// A repo that still has the old file would otherwise lose its exceptions
// without a word; presence is a finding that names the rename.
var renamedFiles = map[string]string{
	"docs/conform.json": ValuesFile,
	"conform.json":      ValuesFile,
}

// checkRetiredFiles reports any retired or renamed file still present.
func checkRetiredFiles(dir string) []Finding {
	var findings []Finding
	for old, now := range renamedFiles {
		if _, err := os.Stat(filepath.Join(dir, old)); err == nil {
			findings = append(findings, Finding{
				File:   old,
				Rule:   RuleRetiredFiles,
				Msg:    "renamed — the tool is now repo-standard and reads " + now + "; the old file is honored only until it moves",
				Repair: fmt.Sprintf("git mv %s %s", old, now),
			})
		}
	}
	for _, rel := range retiredFiles {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleRetiredFiles,
				Msg:    "retired fleet-wide — its job moved into repo-standard; a soft-fail gate left in place reads as protection and provides none",
				Repair: fmt.Sprintf("git rm %s (and drop any Makefile/CI references)", rel),
			})
		}
	}
	return findings
}
