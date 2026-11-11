package checks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The three bd config keys every fleet repo carries (ratified 2026-08-09):
// plan_dir points dispatch audit trails at the vault, the prefix names the
// repo's beads, and sync.remote gives bead history an off-machine recovery
// path.
//
// Surface 1 reads the TRACKED declaration, .beads/config.yaml — not bd's
// live store. The live store is an untracked embedded Dolt database: absent
// from every fresh clone (so CI could never verify it), and reachable only
// through a bd process (whose Dolt lock makes parallel invocations time each
// other out — cfm-1e1.2 dead end). The tracked file is reviewable,
// clone-derivable, and diffable in the PR that changes it. Whether the LIVE
// bd config matches this declaration is machine state — that comparison is
// --local's job (cfm-1e1.3), same split as hooks-shape (files here,
// core.hooksPath there).
const bdConfigFile = ".beads/config.yaml"

// bdKey is one required bd config key: its canonical name, the spellings
// accepted in the tracked file, the repair, and why it matters.
// checkBDConfig verifies each; the scaffold renderer emits each.
type bdKey struct {
	key    string   // canonical name (bd config get spelling)
	paths  []string // accepted spellings in .beads/config.yaml
	repair string
	why    string
}

var bdKeys = []bdKey{
	{
		key:    "custom.plan_dir",
		paths:  []string{"custom.plan_dir"},
		repair: "declare custom.plan_dir: ~/Projects/dk/Project/<repo>/plans in " + bdConfigFile + " (and bd config set to match)",
		why:    "plans and dispatch audit trails have no derivable home",
	},
	{
		key:    "issue_prefix",
		paths:  []string{"issue-prefix", "issue_prefix"},
		repair: "declare issue-prefix: \"<2-3 letters>\" in " + bdConfigFile + " (bd init --prefix set it in the db; mirror it here)",
		why:    "beads mint without a stable repo prefix",
	},
	{
		key:    "sync.remote",
		paths:  []string{"sync.remote"},
		repair: "declare sync.remote: \"git+https://github.com/<owner>/<repo>.git\" in " + bdConfigFile + " (and bd config set to match)",
		why:    "bead history is local-only, no off-machine recovery",
	},
}

// checkBDConfig verifies the three keys are declared non-empty in the
// tracked bd config file (bd-config).
func checkBDConfig(dir string) []Finding {
	doc, err := readBDConfig(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Finding{{
				File:   bdConfigFile,
				Rule:   RuleBDConfig,
				Msg:    "no tracked bd config — plan_dir, prefix, and sync.remote are undeclared",
				Repair: "bd init --prefix <p>, then declare the three keys in " + bdConfigFile,
			}}
		}
		return []Finding{{
			File:   bdConfigFile,
			Rule:   RuleBDConfig,
			Msg:    fmt.Sprintf("unparseable YAML: %v", err),
			Repair: "fix the YAML",
		}}
	}

	var findings []Finding
	for _, k := range bdKeys {
		if lookupAny(doc, k.paths) == "" {
			findings = append(findings, Finding{
				File:   bdConfigFile,
				Rule:   RuleBDConfig,
				Msg:    fmt.Sprintf("%s is undeclared — %s", k.key, k.why),
				Repair: k.repair,
			})
		}
	}
	return findings
}

// readBDConfig loads and parses the tracked bd config file.
func readBDConfig(dir string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(dir, bdConfigFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", bdConfigFile, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", bdConfigFile, err)
	}
	return doc, nil
}

// declaredBDConfig returns the declared values by canonical key name; empty
// map when the file is missing or unparseable (Surface 1 owns that finding).
func declaredBDConfig(dir string) map[string]string {
	declared := make(map[string]string, len(bdKeys))
	doc, err := readBDConfig(dir)
	if err != nil {
		return declared
	}
	for _, k := range bdKeys {
		declared[k.key] = lookupAny(doc, k.paths)
	}
	return declared
}

// lookupAny returns the first non-empty string value found at any of the
// dotted paths. Each path is tried both as a literal flat key
// ("sync.remote":) and as a nested mapping (sync: → remote:) — both shapes
// exist in the fleet.
func lookupAny(doc map[string]any, paths []string) string {
	for _, path := range paths {
		if s, ok := doc[path].(string); ok && s != "" {
			return s
		}
		if s := lookupNested(doc, strings.Split(path, ".")); s != "" {
			return s
		}
	}
	return ""
}

func lookupNested(doc map[string]any, parts []string) string {
	cur := any(doc)
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[part]
	}
	s, _ := cur.(string)
	return s
}
