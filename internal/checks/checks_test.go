package checks_test

import (
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/checks"
)

// TestRun_ConformingRepo: a repo satisfying the whole Surface-1 contract
// produces zero findings.
func TestRun_ConformingRepo(t *testing.T) {
	t.Parallel()

	dir := writeRepo(t, goodRepo())

	findings := checks.Run(dir)
	if len(findings) != 0 {
		t.Fatalf("Run() on a conforming repo returned %d finding(s):\n%v", len(findings), findings)
	}
}

// TestRun_MissingValuesFile: no repo-standard.json is itself a finding, and the
// remaining checks still run under the default tool profile.
func TestRun_MissingValuesFile(t *testing.T) {
	files := goodRepo()
	delete(files, checks.ValuesFile)
	dir := writeRepo(t, files)

	findings := checks.Run(dir)
	rules := rulesOf(findings)
	if rules[checks.RuleValuesFile] != 1 {
		t.Fatalf("want exactly one %s finding, got rules %v", checks.RuleValuesFile, rules)
	}
	if len(findings) != 1 {
		t.Errorf("other checks should pass on the otherwise-good repo, got %v", findings)
	}
}

// TestRun_InvalidValuesFile: the finding carries the values package's
// path-and-field error text.
func TestRun_InvalidValuesFile(t *testing.T) {
	files := goodRepo()
	files[checks.ValuesFile] = `{"profile": "tool", "exceptions": [{"rule": "x"}]}`
	dir := writeRepo(t, files)

	findings := checks.Run(dir)
	var msg string
	for _, f := range findings {
		if f.Rule == checks.RuleValuesFile {
			msg = f.Msg
		}
	}
	if msg == "" {
		t.Fatalf("want a %s finding, got %v", checks.RuleValuesFile, findings)
	}
	for _, part := range []string{"docs/repo-standard.json", `rule "x"`, "reason"} {
		if !strings.Contains(msg, part) {
			t.Errorf("finding message %q should name %q", msg, part)
		}
	}
}

// TestRun_ExceptionSuppressesFinding: a declared, reasoned exception is the
// one sanctioned suppression.
func TestRun_ExceptionSuppressesFinding(t *testing.T) {
	files := goodRepo()
	// Break the codex shape…
	files[".github/workflows/codex-review.yml"] = strings.Replace(
		goodCodexYML, "issue_comment:\n    types: [created]", "pull_request:", 1)
	// …and declare the exception.
	files[checks.ValuesFile] = `{"profile": "tool", "exceptions": [
		{"rule": "codex-workflow", "reason": "this repo pays for auto-review deliberately"}]}`
	dir := writeRepo(t, files)

	findings := checks.Run(dir)
	if n := rulesOf(findings)[checks.RuleCodexShape]; n != 0 {
		t.Errorf("excepted rule still produced %d finding(s): %v", n, findings)
	}

	// Same break without the exception must fail.
	files[checks.ValuesFile] = goodValues
	dir = writeRepo(t, files)
	if n := rulesOf(checks.Run(dir))[checks.RuleCodexShape]; n == 0 {
		t.Error("codex-workflow break produced no finding without the exception")
	}
}

// TestRun_LegacyValuesFileStillHonored: a repo that still holds its exceptions
// under the tool's old name (docs/conform.json, or conform.json at the root)
// keeps them until its own pin bump moves the file.
//
// This is the rename's whole risk. Every fleet repo holds an old-name file, and
// a cutover that read only docs/repo-standard.json would drop their exceptions
// the next time repo-standard ran, every rule they had excused firing at once,
// with the one finding that explains why buried among them. Read the old file,
// and report it as retired: the repo keeps working and still learns it must move.
func TestRun_LegacyValuesFileStillHonored(t *testing.T) {
	for _, old := range checks.LegacyValuesFiles {
		t.Run(old, func(t *testing.T) {
			files := goodRepo()
			// Break the codex shape…
			files[".github/workflows/codex-review.yml"] = strings.Replace(
				goodCodexYML, "issue_comment:\n    types: [created]", "pull_request:", 1)
			// …and declare the exception under the old name.
			delete(files, checks.ValuesFile)
			files[old] = `{"profile": "tool", "exceptions": [
		{"rule": "codex-workflow", "reason": "this repo pays for auto-review deliberately"}]}`
			dir := writeRepo(t, files)

			findings := checks.Run(dir)
			if n := rulesOf(findings)[checks.RuleCodexShape]; n != 0 {
				t.Errorf("%s exception ignored: %d finding(s): %v", old, n, findings)
			}
			// The old file is read, not blessed: it must be renamed.
			if n := rulesOf(findings)[checks.RuleRetiredFiles]; n == 0 {
				t.Errorf("%s produced no retired-files finding", old)
			}
			// And it must not ALSO be reported as a missing values file.
			if n := rulesOf(findings)[checks.RuleValuesFile]; n != 0 {
				t.Errorf("values-file finding raised despite a readable %s: %v", old, findings)
			}
		})
	}
}

// TestRun_InvalidDocsValuesFileDoesNotFallBack: a docs/repo-standard.json that is
// present but broken is reported as broken. Quietly using the root copy instead
// would hide the typo behind stale values — the fallback covers a file that is
// missing, never one that is wrong.
func TestRun_InvalidDocsValuesFileDoesNotFallBack(t *testing.T) {
	files := goodRepo()
	files[checks.ValuesFile] = `{"profile": "tool", "exceptions": [{"rule": "x"}]}`
	files[checks.LegacyValuesFiles[1]] = goodValues
	dir := writeRepo(t, files)

	if n := rulesOf(checks.Run(dir))[checks.RuleValuesFile]; n == 0 {
		t.Error("invalid docs/repo-standard.json was masked by the root fallback")
	}
}

// TestRun_LibProfile: libs are the contract minus deploy — a deploy verb is
// a finding, its absence is not.
func TestRun_LibProfile(t *testing.T) {
	files := goodRepo()
	files[checks.ValuesFile] = `{"profile": "lib", "exceptions": []}`

	// Lib with deploy: finding.
	dir := writeRepo(t, files)
	if n := rulesOf(checks.Run(dir))[checks.RuleMakefileVerb]; n == 0 {
		t.Error("lib profile with a deploy verb produced no makefile-verbs finding")
	}

	// Lib without deploy: clean.
	files["Makefile"] = strings.Replace(goodMakefile, "deploy: build ## Install locally\n", "", 1)
	dir = writeRepo(t, files)
	if findings := checks.Run(dir); len(findings) != 0 {
		t.Errorf("deploy-less lib should be clean, got %v", findings)
	}
}

// TestFindingString: the printed form names file, rule, and repair — the
// contract every failure must honor.
func TestFindingString(t *testing.T) {
	t.Parallel()

	f := checks.Finding{File: "Makefile", Rule: "makefile-verbs", Msg: "m", Repair: "r"}
	s := f.String()
	for _, part := range []string{"Makefile", "makefile-verbs", "m", "fix: r"} {
		if !strings.Contains(s, part) {
			t.Errorf("String() = %q, missing %q", s, part)
		}
	}
}
