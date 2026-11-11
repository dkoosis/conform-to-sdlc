package checks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dkoosis/repo-standard/internal/lintpack"
)

// packRepair is the command that rewrites a drifted or partial pack.
const rawStateWriteFile = "optin_rawstatewrite.go"

const packRepair = "go tool repo-standard pack sync (a rule change belongs upstream in dkoosis/lintbrush gorules/, then a repo-standard release)"

// checkLintPack compares the repo's .golangci-rules/ with the lintbrush pack
// repo-standard embeds, byte for byte (lint-pack). The pack is Go source
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
			Repair: "go tool repo-standard --fix (writes the pack when .golangci-rules/ is absent), then enable gocritic's ruleguard check on .golangci-rules/bugclasses.go and modern.go",
		})
	}

	commit := "unknown"
	if src, err := lintpack.ReadSource(); err == nil && len(src.Commit) >= 12 {
		commit = src.Commit[:12]
	}

	usesAtomicfile := lintpack.UsesAtomicfile(dir)
	for _, m := range lintpack.Members {
		if m.Repo == "" {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(lintpack.Dir, m.Repo))
		want, err := lintpack.Bytes(m.Source)
		if err != nil {
			// An unreadable embed is repo-standard breaking, not the repo.
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleLintPack,
				Msg:    "repo-standard cannot read its embedded lint rules pack: " + err.Error(),
				Repair: "file a bug against repo-standard — the repo is not at fault",
			})
			continue
		}
		have, err := os.ReadFile(filepath.Join(dir, rel))
		switch {
		case err != nil && !m.Wanted(usesAtomicfile):
			continue
		case err != nil:
			findings = append(findings, missingPackFile(m, rel))
		case !bytes.Equal(have, want):
			findings = append(findings, Finding{
				File:   rel,
				Rule:   RuleLintPack,
				Msg:    "differs from lintbrush gorules/" + m.Source + " at " + commit + ", the pack repo-standard ships",
				Repair: packRepair,
			})
		}
	}
	return append(findings, rawStateWriteGlobFindings(dir, usesAtomicfile)...)
}

// missingPackFile is the finding for a pack file the repo must hold and does
// not. The atomicfile-gated file says why this repo needs it.
func missingPackFile(m lintpack.File, rel string) Finding {
	if m.Required {
		return Finding{
			File:   rel,
			Rule:   RuleLintPack,
			Msg:    "missing from the lint rules pack (lintbrush gorules/" + m.Source + ")",
			Repair: packRepair,
		}
	}
	return Finding{
		File:   rel,
		Rule:   RuleLintPack,
		Msg:    "missing: this repo requires " + lintpack.AtomicfileModule + ", so it must run pack rule 2 (raw os.WriteFile of durable state), and the rule lives in lintbrush gorules/" + m.Source,
		Repair: packRepair + "; then list " + rel + " in gocritic's ruleguard rules",
	}
}

// rawStateWriteGlobFindings reports a repo that requires atomicfile whose
// gocritic ruleguard `rules` glob does not list the raw-state-write rule file:
// the file is there but the rule never runs. A config that is absent or does
// not parse is lint-set's finding, not this one.
func rawStateWriteGlobFindings(dir string, usesAtomicfile bool) []Finding {
	if !usesAtomicfile {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, golangciFile))
	if err != nil {
		return nil
	}
	var cfg lintSetConfig
	if yaml.Unmarshal(data, &cfg) != nil {
		return nil
	}
	rules := cfg.Linters.Settings.Gocritic.Settings.Ruleguard.Rules
	if strings.Contains(rules, lintpack.Dir+"/"+rawStateWriteFile) {
		return nil
	}
	return []Finding{{
		File:   golangciFile,
		Rule:   RuleLintPack,
		Msg:    "linters.settings.gocritic.settings.ruleguard.rules does not name " + lintpack.Dir + "/" + rawStateWriteFile + " — this repo requires " + lintpack.AtomicfileModule + ", and pack rule 2 (raw os.WriteFile of durable state) never runs",
		Repair: `append ",${base-path}/` + lintpack.Dir + "/" + rawStateWriteFile + `" to linters.settings.gocritic.settings.ruleguard.rules`,
	}}
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
