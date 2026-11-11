package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The baseline floor (decision d7ea466a8172, shipped shape ccp-sbp.2):
// linters.default: standard, plus these six explicitly guaranteed. errcheck
// and staticcheck ride in via `standard`; staticcheck is ALSO named in
// enable so the floor survives a future default change; nolintlint must
// require explanation + specific codes. contextcheck is deliberately demoted
// — FP-dominated on all nine mains; its defect class is owned path-scoped by
// forbidigo rule 5 — so its presence is a finding, not a requirement.
var (
	// floorEnable must each appear in linters.enable.
	floorEnable = []string{"nilerr", "rowserrcheck", "noctx", "staticcheck", "nolintlint"}
	// floorLinters must not appear in linters.disable (errcheck arrives via
	// default: standard, so disable is the only way to lose it).
	floorLinters = []string{"errcheck", "nilerr", "rowserrcheck", "noctx", "staticcheck", "nolintlint"}
)

// golangciConfig is the subset of .golangci.yml the floor check reads. The
// comparison is semantic — parsed sets, never bytes — so per-repo extras and
// ordering are free.
type golangciConfig struct {
	Linters struct {
		Default  string   `yaml:"default"`
		Enable   []string `yaml:"enable"`
		Disable  []string `yaml:"disable"`
		Settings struct {
			Nolintlint struct {
				RequireExplanation bool `yaml:"require-explanation"`
				RequireSpecific    bool `yaml:"require-specific"`
			} `yaml:"nolintlint"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

// checkLintFloor verifies .golangci.yml carries the fleet baseline floor as
// a parsed subset (lint-floor) and that contextcheck stays demoted
// (lint-contextcheck).
func checkLintFloor(dir string) []Finding {
	const file = ".golangci.yml"
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return []Finding{{
			File:   file,
			Rule:   RuleLintFloor,
			Msg:    "no .golangci.yml — the repo runs without the fleet lint floor",
			Repair: "copy the baseline (dkoosis/lintbrush gorules/golangci.baseline.yml) and add per-repo extras",
		}}
	}

	var cfg golangciConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return []Finding{{
			File:   file,
			Rule:   RuleLintFloor,
			Msg:    fmt.Sprintf("unparseable YAML: %v", err),
			Repair: "fix the YAML; conform-to-sdlc compares parsed sets, not bytes",
		}}
	}

	var findings []Finding
	add := func(rule, msg, repair string) {
		findings = append(findings, Finding{File: file, Rule: rule, Msg: msg, Repair: repair})
	}

	if cfg.Linters.Default != "standard" {
		add(RuleLintFloor,
			fmt.Sprintf("linters.default is %q, want \"standard\" — the floor rides on golangci's standard set", cfg.Linters.Default),
			"set linters.default: standard",
		)
	}

	enabled := toSet(cfg.Linters.Enable)
	if missing := missingFrom(cfg.Linters.Enable, floorEnable); len(missing) > 0 {
		add(RuleLintFloor,
			"baseline floor linters missing from linters.enable: "+strings.Join(missing, ", "),
			"add to linters.enable: "+strings.Join(missing, ", "),
		)
	}

	disabled := toSet(cfg.Linters.Disable)
	var offFloor []string
	for _, l := range floorLinters {
		if disabled[l] {
			offFloor = append(offFloor, l)
		}
	}
	if len(offFloor) > 0 {
		add(RuleLintFloor,
			fmt.Sprintf("floor linters disabled: %s — the floor is the contract, extras are the freedom", strings.Join(offFloor, ", ")),
			"remove from linters.disable: "+strings.Join(offFloor, ", "),
		)
	}

	nl := cfg.Linters.Settings.Nolintlint
	if enabled["nolintlint"] && (!nl.RequireExplanation || !nl.RequireSpecific) {
		add(RuleLintFloor,
			"nolintlint must require an explanation and a specific linter on every //nolint",
			"set linters.settings.nolintlint: require-explanation: true, require-specific: true",
		)
	}

	if enabled["contextcheck"] {
		add(RuleLintContext,
			"contextcheck enabled — reintroduces scoped-away noise (FP-dominated fleet-wide; the defect class is owned path-scoped by forbidigo rule 5)",
			"remove contextcheck from linters.enable",
		)
	}

	return findings
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, it := range items {
		set[it] = true
	}
	return set
}
