package checks

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dkoosis/conform-to-sdlc/internal/values"
)

// CanonicalGolangci is the fleet's canonical .golangci.yml: the linter set
// lint-set requires and the settings it holds strict. --fix and init write it
// into a repo that has none.
//
//go:embed golangci.canonical.yml
var CanonicalGolangci string

// golangciFile is the config every lint rule reads.
const golangciFile = ".golangci.yml"

// standardLinters is what golangci-lint v2's `default: standard` enables.
var standardLinters = []string{"errcheck", "govet", "ineffassign", "staticcheck", "unused"}

// lintSetConfig is the part of .golangci.yml lint-set reads. Parsed, never
// compared as bytes: order, comments and extras are the repo's own.
type lintSetConfig struct {
	Linters struct {
		Default  string   `yaml:"default"`
		Enable   []string `yaml:"enable"`
		Disable  []string `yaml:"disable"`
		Settings struct {
			Errcheck struct {
				ExcludeFunctions []string `yaml:"exclude-functions"`
			} `yaml:"errcheck"`
			Nolintlint struct {
				RequireExplanation bool `yaml:"require-explanation"`
				RequireSpecific    bool `yaml:"require-specific"`
				AllowUnused        bool `yaml:"allow-unused"`
			} `yaml:"nolintlint"`
			Gocritic struct {
				EnabledChecks []string `yaml:"enabled-checks"`
				Settings      struct {
					Ruleguard struct {
						Rules  string `yaml:"rules"`
						FailOn string `yaml:"failOn"`
					} `yaml:"ruleguard"`
				} `yaml:"settings"`
			} `yaml:"gocritic"`
			Forbidigo struct {
				AnalyzeTypes bool         `yaml:"analyze-types"`
				Forbid       []forbidRule `yaml:"forbid"`
			} `yaml:"forbidigo"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

// forbidRule is one forbidigo pattern.
type forbidRule struct {
	Pattern string `yaml:"pattern"`
}

// enabled is the set of linters the config turns on.
func (c lintSetConfig) enabled(all []string) map[string]bool {
	on := map[string]bool{}
	switch c.Linters.Default {
	case "standard", "":
		for _, l := range standardLinters {
			on[l] = true
		}
	case "all":
		for _, l := range all {
			on[l] = true
		}
	}
	for _, l := range c.Linters.Enable {
		on[l] = true
	}
	for _, l := range c.Linters.Disable {
		delete(on, l)
	}
	return on
}

// canonicalSet parses the embedded canonical config.
func canonicalSet() (lintSetConfig, map[string]bool, error) {
	var c lintSetConfig
	if err := yaml.Unmarshal([]byte(CanonicalGolangci), &c); err != nil {
		return c, nil, fmt.Errorf("parse the canonical .golangci.yml: %w", err)
	}
	return c, c.enabled(nil), nil
}

// lintSetDrops collects the linters the repo drops by reason in
// docs/conform.json, and a finding for each drop that carries no reason.
func lintSetDrops(vals values.Values, canon map[string]bool) (map[string]bool, []Finding) {
	drops := map[string]bool{}
	var findings []Finding
	for _, e := range vals.Exceptions {
		if e.Rule != RuleLintSet {
			continue
		}
		for _, l := range sortedKeys(e.Drop) {
			switch {
			case strings.TrimSpace(e.Drop[l]) == "":
				findings = append(findings, Finding{
					File:   ValuesFile,
					Rule:   RuleLintSet,
					Msg:    fmt.Sprintf("drops %s with no reason — a linter leaves the set only with its why written down", l),
					Repair: fmt.Sprintf(`write the reason: {"rule": "lint-set", "drop": {%q: "<why this repo cannot run it>"}}`, l),
				})
			case !canon[l]:
				findings = append(findings, Finding{
					File:   ValuesFile,
					Rule:   RuleLintSet,
					Msg:    fmt.Sprintf("drops %s, which is not in the canonical set — the entry excuses nothing", l),
					Repair: fmt.Sprintf("delete %q from the lint-set drop", l),
				})
			default:
				drops[l] = true
			}
		}
	}
	return drops, findings
}

// checkLintSet fails a repo whose .golangci.yml does not enable every linter
// in the canonical set, or that weakens a setting the set holds strict
// (lint-set; epic cfm-pxx R2, decisions 284ca53cd396 and d7ea466a8172). A repo
// may enable more. It leaves one out only by name and reason in
// docs/conform.json: {"rule": "lint-set", "drop": {"<linter>": "<reason>"}}.
//
// lint-floor still guards the six-linter floor on its own: a repo cannot drop
// a floor linter through lint-set.
func checkLintSet(dir string, vals values.Values) []Finding {
	canon, canonOn, err := canonicalSet()
	if err != nil {
		return []Finding{{
			File:   golangciFile,
			Rule:   RuleLintSet,
			Msg:    "conform-to-sdlc cannot parse its embedded canonical .golangci.yml: " + err.Error(),
			Repair: "file a bug against conform-to-sdlc — the repo is not at fault",
		}}
	}
	drops, findings := lintSetDrops(vals, canonOn)

	data, err := os.ReadFile(filepath.Join(dir, golangciFile))
	if err != nil {
		return append(findings, Finding{
			File:   golangciFile,
			Rule:   RuleLintSet,
			Msg:    "no .golangci.yml — the repo runs none of the canonical linter set",
			Repair: "go tool conform-to-sdlc --fix (writes the canonical .golangci.yml when none exists)",
		})
	}
	var cfg lintSetConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return append(findings, Finding{
			File:   golangciFile,
			Rule:   RuleLintSet,
			Msg:    fmt.Sprintf("unparseable YAML: %v", err),
			Repair: "fix the YAML; lint-set compares parsed linter sets, not bytes",
		})
	}

	on := cfg.enabled(sortedKeys(canonOn))
	var missing []string
	for _, l := range sortedKeys(canonOn) {
		if !on[l] && !drops[l] {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		findings = append(findings, Finding{
			File:   golangciFile,
			Rule:   RuleLintSet,
			Msg:    "canonical linters not enabled: " + strings.Join(missing, ", "),
			Repair: "add to linters.enable (and remove from linters.disable): " + strings.Join(missing, ", ") + `; or drop one with its reason in ` + ValuesFile + `: {"rule": "lint-set", "drop": {"<linter>": "<reason>"}}`,
		})
	}

	for _, w := range weakSettings(cfg, canon, on) {
		findings = append(findings, Finding{File: golangciFile, Rule: RuleLintSet, Msg: w.msg, Repair: w.repair})
	}
	return findings
}

type weakSetting struct{ msg, repair string }

func weak(setting, why, repair string) weakSetting {
	return weakSetting{msg: setting + " " + why, repair: repair}
}

// weakSettings names each setting that switches an enabled canonical linter
// partly off. A linter the repo does not run is reported as missing, not here.
func weakSettings(cfg, canon lintSetConfig, on map[string]bool) []weakSetting {
	var out []weakSetting
	if on["nolintlint"] {
		out = append(out, weakNolintlint(cfg)...)
	}
	if on["errcheck"] {
		out = append(out, weakErrcheck(cfg, canon)...)
	}
	if on["gocritic"] {
		out = append(out, weakRuleguard(cfg)...)
	}
	if on["forbidigo"] {
		out = append(out, weakForbidigo(cfg, canon)...)
	}
	return out
}

func weakNolintlint(cfg lintSetConfig) []weakSetting {
	n := cfg.Linters.Settings.Nolintlint
	var out []weakSetting
	if !n.RequireExplanation {
		out = append(out, weak("linters.settings.nolintlint.require-explanation", "is not true — a //nolint may skip its reason", "set linters.settings.nolintlint.require-explanation: true"))
	}
	if !n.RequireSpecific {
		out = append(out, weak("linters.settings.nolintlint.require-specific", "is not true — a //nolint may silence every linter at once", "set linters.settings.nolintlint.require-specific: true"))
	}
	if n.AllowUnused {
		out = append(out, weak("linters.settings.nolintlint.allow-unused", "is true — a //nolint that no longer silences anything stays", "delete linters.settings.nolintlint.allow-unused"))
	}
	return out
}

func weakErrcheck(cfg, canon lintSetConfig) []weakSetting {
	allowed := toSet(canon.Linters.Settings.Errcheck.ExcludeFunctions)
	var extra []string
	for _, f := range cfg.Linters.Settings.Errcheck.ExcludeFunctions {
		if !allowed[f] {
			extra = append(extra, f)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return []weakSetting{weak("linters.settings.errcheck.exclude-functions", "lets errors drop from "+strings.Join(extra, ", ")+" — beyond the canonical list of calls whose error is harmless", "remove from exclude-functions: "+strings.Join(extra, ", ")+" (check the error, or //nolint:errcheck the one call with its reason)")}
}

func weakRuleguard(cfg lintSetConfig) []weakSetting {
	gc := cfg.Linters.Settings.Gocritic
	var out []weakSetting
	if !slices.Contains(gc.EnabledChecks, "ruleguard") {
		out = append(out, weak("linters.settings.gocritic.enabled-checks", "does not hold ruleguard — the lintbrush rules pack never runs", "add ruleguard to linters.settings.gocritic.enabled-checks"))
	}
	var unwired []string
	for _, f := range []string{"bugclasses.go", "modern.go"} {
		if !strings.Contains(gc.Settings.Ruleguard.Rules, ".golangci-rules/"+f) {
			unwired = append(unwired, f)
		}
	}
	if len(unwired) > 0 {
		out = append(out, weak("linters.settings.gocritic.settings.ruleguard.rules", "does not name .golangci-rules/"+strings.Join(unwired, ", .golangci-rules/")+" — those pack rules never run", `set rules: "${base-path}/.golangci-rules/bugclasses.go,${base-path}/.golangci-rules/modern.go"`))
	}
	if !strings.Contains(gc.Settings.Ruleguard.FailOn, "dsl") {
		out = append(out, weak("linters.settings.gocritic.settings.ruleguard.failOn", "does not hold dsl — a rule that fails to compile is skipped in silence", "set linters.settings.gocritic.settings.ruleguard.failOn: dsl"))
	}
	return out
}

func weakForbidigo(cfg, canon lintSetConfig) []weakSetting {
	fb := cfg.Linters.Settings.Forbidigo
	var out []weakSetting
	for _, w := range canon.Linters.Settings.Forbidigo.Forbid {
		if !slices.Contains(fb.Forbid, w) {
			out = append(out, weak("linters.settings.forbidigo.forbid", "lacks the pattern "+w.Pattern+" — pack rule 5 (context.Background deep in a daemon) never runs", "add to linters.settings.forbidigo.forbid: - pattern: '"+w.Pattern+"' (scope it by path in exclusions)"))
		}
	}
	if !fb.AnalyzeTypes {
		out = append(out, weak("linters.settings.forbidigo.analyze-types", "is not true — the pattern matches by text and misfires on a ctx.Background field", "set linters.settings.forbidigo.analyze-types: true"))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
