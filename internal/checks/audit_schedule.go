package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Nightly audit (cfm-9uq, epic cfm-pxx R5): the slow gates (race, fuzz, dupe,
// nilcheck, deadcode, vuln) run in CI on a schedule, so they run when nobody
// remembers to. The rule reads every workflow under .github/workflows.

// auditWorkflowFile is the workflow the scaffold and --fix write for it.
const auditWorkflowFile = ".github/workflows/audit.yml"

// makeAudit finds a `make audit` command (make's own flags may sit between) on a run line that is not a comment.
var makeAudit = regexp.MustCompile(`(?m)^[^#\n]*\bmake\b[^#\n;&|]*\saudit(?:[\s;&|]|$)`)

// checkCIAuditSchedule fails a repo no workflow of which runs `make audit` on
// a schedule: a schedule trigger with a cron entry, and a step running make
// audit that the schedule event does not skip with an if.
func checkCIAuditSchedule(dir string) []Finding {
	paths, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml"))
	if slices.ContainsFunc(paths, workflowRunsAuditOnSchedule) {
		return nil
	}
	return []Finding{{
		File:   filepath.ToSlash(filepath.Join(".github", "workflows")),
		Rule:   RuleCIAuditSchedule,
		Msg:    "no workflow runs `make audit` on a schedule — race, fuzz, dupe, nilcheck, deadcode and vuln run only when someone remembers to",
		Repair: "add " + auditWorkflowFile + " with `on: schedule: - cron: '41 7 * * *'` and a step running `make audit` (conform-to-sdlc --fix writes one when the repo has none)",
	}}
}

// workflowRunsAuditOnSchedule says whether one workflow file has a schedule
// trigger and a make audit step the schedule event reaches.
func workflowRunsAuditOnSchedule(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var wf workflow
	if yaml.Unmarshal(data, &wf) != nil || !hasCron(&wf.On) {
		return false
	}
	for _, job := range wf.Jobs {
		if !reachesSchedule(job.If) {
			continue
		}
		for _, step := range job.Steps {
			if makeAudit.MatchString(step.Run) && reachesSchedule(step.If) {
				return true
			}
		}
	}
	return false
}

// reachesSchedule says whether a job or step `if` lets the schedule event
// through: no condition, one that never tests the event (a repository check,
// always()), or one that names the schedule event without negating it.
func reachesSchedule(cond string) bool {
	cond = strings.TrimSpace(cond)
	switch {
	case cond == "" || !strings.Contains(cond, "event_name"):
		return true
	case strings.Contains(cond, "!= 'schedule'") || strings.Contains(cond, `!= "schedule"`):
		return false
	}
	return strings.Contains(cond, "'schedule'") || strings.Contains(cond, `"schedule"`)
}

// hasCron says whether the `on:` node carries a schedule with a cron entry.
func hasCron(on *yaml.Node) bool {
	if on.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(on.Content); i += 2 {
		if on.Content[i].Value != "schedule" {
			continue
		}
		for _, entry := range on.Content[i+1].Content {
			for j := 0; j+1 < len(entry.Content); j += 2 {
				if entry.Content[j].Value == "cron" && strings.TrimSpace(entry.Content[j+1].Value) != "" {
					return true
				}
			}
		}
	}
	return false
}

// fixAuditWorkflow writes the nightly audit workflow when no workflow runs
// make audit on a schedule and audit.yml is absent. An existing file is a
// person's to repair.
func fixAuditWorkflow(dir string) (string, error) {
	if len(checkCIAuditSchedule(dir)) == 0 {
		return "", nil
	}
	path := filepath.Join(dir, auditWorkflowFile)
	if _, err := os.Stat(path); err == nil {
		return "", nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(renderAuditWorkflow(ScaffoldSpec{})), 0o600); err != nil {
		return "", err
	}
	return fmt.Sprintf("created %s — make audit nightly", auditWorkflowFile), nil
}

// renderAuditWorkflow emits the nightly audit: the check workflow's setup,
// then make audit, on a cron and on demand.
func renderAuditWorkflow(ScaffoldSpec) string {
	return strings.ReplaceAll(auditWorkflowTmpl, "@PIN@", pinFile)
}

const auditWorkflowTmpl = `name: audit

# The slow gates (race, fuzz, dupe, nilcheck, deadcode, vuln), nightly and on
# demand. conform-to-sdlc's ci-audit-schedule rule needs a cron here and a
# step that runs make audit.
on:
  schedule:
    - cron: '41 7 * * *' # daily; off the hour to dodge GitHub's top-of-hour queue
  workflow_dispatch:

permissions:
  contents: read

jobs:
  audit:
    runs-on: ubuntu-latest
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          check-latest: false

      # dupe runs jscpd through npx.
      - uses: actions/setup-node@v4
        with:
          node-version: '22'

      - name: install linter (version pinned in @PIN@)
        run: |
          . ./@PIN@
          curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
            | sh -s -- -b "$(go env GOPATH)/bin" "$GOLANGCI_LINT_VERSION"
          echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"

      - name: audit
        run: make audit
`
