package checks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/dkoosis/conform-to-sdlc/internal/lintpack"
)

// packRepair is the command that rewrites a drifted or partial pack.
const packRepair = "go tool conform-to-sdlc pack sync (a rule change belongs upstream in dkoosis/lintbrush gorules/, then a conform-to-sdlc release)"

// checkLintPack compares the repo's .golangci-rules/ with the lintbrush pack
// conform-to-sdlc embeds, byte for byte (lint-pack). The pack is Go source
// shipped whole, so bytes are the right comparison here; the README's
// semantic-comparison principle is about the YAML lint core. No network call:
// the copy that used to be fetched to compare against is now in the binary.
//
// One finding per file, because the repair is per file. A required file that
// is missing is a finding, so a partial pack never reads as whole. An opt-in
// file is checked only where the repo holds it.
//
// It also reports, under makefile-verbs, a Makefile that still calls the
// retired check-pack-drift.sh: lint-pack replaced it.
func checkLintPack(dir string) []Finding {
	findings := packDriftCallFindings(dir)

	if info, err := os.Stat(filepath.Join(dir, lintpack.Dir)); err != nil || !info.IsDir() {
		return append(findings, Finding{
			File:   lintpack.Dir + "/",
			Rule:   RuleLintPack,
			Msg:    "no lint rules pack — the ruleguard rules for the fleet's recurring Go defect classes do not run here",
			Repair: "go tool conform-to-sdlc --fix (writes the pack when .golangci-rules/ is absent), then wire it into .golangci.yml",
		})
	}

	commit := "unknown"
	if src, err := lintpack.ReadSource(); err == nil && len(src.Commit) >= 12 {
		commit = src.Commit[:12]
	}

	for _, m := range lintpack.Members {
		if m.Repo == "" {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(lintpack.Dir, m.Repo))
		want, err := lintpack.Bytes(m.Source)
		if err != nil {
			// An unreadable embed is conform-to-sdlc breaking, not the repo.
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleLintPack,
				Msg:    "conform-to-sdlc cannot read its embedded lint rules pack: " + err.Error(),
				Repair: "file a bug against conform-to-sdlc — the repo is not at fault",
			})
			continue
		}
		have, err := os.ReadFile(filepath.Join(dir, rel))
		switch {
		case err != nil && !m.Required:
			continue
		case err != nil:
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleLintPack,
				Msg:    "missing from the lint rules pack (lintbrush gorules/" + m.Source + ")",
				Repair: packRepair,
			})
		case !bytes.Equal(have, want):
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleLintPack,
				Msg:    "differs from lintbrush gorules/" + m.Source + " at " + commit + ", the pack conform-to-sdlc ships",
				Repair: packRepair,
			})
		}
	}
	return findings
}

// packDriftCallFindings reports each Makefile line, outside a comment, that
// runs check-pack-drift.sh. The script warned and exited 0 once its upstream
// died; a make step calling it reads as a pack check and performs none.
func packDriftCallFindings(dir string) []Finding {
	data, err := os.ReadFile(filepath.Join(dir, makefileFile))
	if err != nil {
		return nil // checkMakefile reports a missing Makefile
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "check-pack-drift.sh") {
			return []Finding{{
				File:   makefileFile,
				Rule:   RuleMakefileVerb,
				Msg:    "a target runs check-pack-drift.sh, which is retired — it exits 0 against a dead upstream; lint-pack checks the pack now",
				Repair: "delete the pack-drift target and drop it from check's prerequisites",
			}}
		}
	}
	return nil
}
