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
var renamedFiles = []string{"docs/conform.json", "conform.json"}

// checkRetiredFiles reports any retired or renamed file still present.
func checkRetiredFiles(dir string) []Finding {
	var findings []Finding
	moved := fileExists(filepath.Join(dir, ValuesFile)) // new file exists: the old one is dead weight
	for _, old := range renamedFiles {
		if !fileExists(filepath.Join(dir, old)) {
			continue
		}
		f := Finding{
			File: old,
			Rule: RuleRetiredFiles,
			Msg:  "renamed — the tool is now repo-standard and reads " + ValuesFile + "; with no such file it falls back to the first old-name file only until that moves",
		}
		if moved || old != renamedFiles[0] && fileExists(filepath.Join(dir, renamedFiles[0])) {
			f.Repair = fmt.Sprintf("fold any exceptions you still need into %s, then git rm %s", ValuesFile, old)
		} else {
			f.Repair = fmt.Sprintf("git mv %s %s", old, ValuesFile)
		}
		findings = append(findings, f)
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
