package checks_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

func auditFindings(t *testing.T, workflows map[string]string) []checks.Finding {
	t.Helper()
	files := map[string]string{}
	for name, body := range workflows {
		files[".github/workflows/"+name] = body
	}
	return checks.CheckCIAuditSchedule(writeRepo(t, files))
}

func TestAuditSchedule_Fails(t *testing.T) {
	t.Parallel()
	tests := map[string]map[string]string{
		"no workflow at all":             {},
		"only the check workflow":        {"check.yml": goodCheckYML},
		"schedule without make audit":    {"audit.yml": "name: a\non:\n  schedule:\n    - cron: '1 2 * * *'\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: make check\n"},
		"make audit without a schedule":  {"audit.yml": "name: a\non:\n  pull_request:\n  workflow_dispatch:\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: make audit\n"},
		"make audit only in a comment":   {"audit.yml": "name: a\non:\n  schedule:\n    - cron: '1 2 * * *'\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: |\n          # make audit\n          make check\n"},
		"audit step skipped on schedule": {"audit.yml": "name: a\non:\n  schedule:\n    - cron: '1 2 * * *'\n  push:\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: make audit\n        if: github.event_name == 'push'\n"},
		"schedule with no cron":          {"audit.yml": "name: a\non:\n  schedule: []\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: make audit\n"},
	}
	for name, wfs := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := auditFindings(t, wfs)
			if len(got) != 1 || got[0].Rule != checks.RuleCIAuditSchedule || !strings.Contains(got[0].Msg, "make audit") || !strings.Contains(got[0].Repair, "cron") {
				t.Fatalf("want one ci-audit-schedule naming make audit and the cron repair, got %v", got)
			}
		})
	}
}

func TestAuditSchedule_Passes(t *testing.T) {
	t.Parallel()
	tests := map[string]map[string]string{
		"dedicated audit workflow": {"audit.yml": goodAuditYML},
		// sdlc's shape: one workflow, a cron, and an audit step guarded on the schedule event.
		"check workflow with a scheduled audit step": {"check.yml": "name: check\non:\n  push:\n  schedule:\n    - cron: '17 9 * * *'\n  workflow_dispatch:\njobs:\n  check:\n    runs-on: x\n    steps:\n      - run: make check\n        if: github.event_name == 'push'\n      - run: make audit # daily\n        if: github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'\n"},
		"make flags before the target":               {"audit.yml": strings.Replace(goodAuditYML, "make audit", "make -o latency audit", 1)},
		"yaml extension":                             {"nightly.yaml": goodAuditYML},
	}
	for name, wfs := range tests {
		if got := auditFindings(t, wfs); len(got) != 0 {
			t.Errorf("%s: produced %v", name, got)
		}
	}
}

// --fix writes the nightly workflow when the repo has none, leaves an
// existing audit.yml alone, and a second run is silent.
func TestAuditSchedule_Fix(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, map[string]string{".github/workflows/check.yml": goodCheckYML})
	done, err := checks.Fix(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !anyContains(done, ".github/workflows/audit.yml") {
		t.Fatalf("Fix reported no audit.yml: %v", done)
	}
	if got := checks.CheckCIAuditSchedule(dir); len(got) != 0 {
		t.Errorf("the written workflow still fails the rule: %v", got)
	}
	again, _ := checks.Fix(dir)
	if anyContains(again, "audit.yml") {
		t.Errorf("second Fix touched audit.yml: %v", again)
	}

	custom := "name: audit\non: pull_request\njobs: {}\n"
	dir = writeRepo(t, map[string]string{".github/workflows/audit.yml": custom})
	if _, err := checks.Fix(dir); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".github", "workflows", "audit.yml"))
	if string(body) != custom {
		t.Errorf("Fix overwrote an existing audit.yml")
	}
}

// conform's own repo carries the nightly workflow its rule asks for, and it
// is the one the scaffold writes.
func TestAuditSchedule_OwnWorkflowMatchesScaffold(t *testing.T) {
	t.Parallel()
	own, err := os.ReadFile("../../.github/workflows/audit.yml")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeRepo(t, map[string]string{".github/workflows/audit.yml": string(own)})
	if got := checks.CheckCIAuditSchedule(dir); len(got) != 0 {
		t.Errorf("conform's own audit.yml fails the rule: %v", got)
	}
	empty := writeRepo(t, nil)
	if _, err := checks.Fix(empty); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(filepath.Join(empty, ".github", "workflows", "audit.yml"))
	if !bytes.Equal(written, own) {
		t.Error("conform's audit.yml differs from the scaffold's")
	}
}
