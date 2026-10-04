package checks_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// ghStub answers gh API paths from a canned map; absent path = 404.
func ghStub(responses map[string]string) func(context.Context, string) ([]byte, error) {
	return func(_ context.Context, path string) ([]byte, error) {
		body, ok := responses[path]
		if !ok {
			return nil, checks.ErrNotFound
		}
		return []byte(body), nil
	}
}

// conformingRepoResponses is a repo with every GitHub-side setting per the
// fleet contract.
func conformingRepoResponses(full string) map[string]string {
	return map[string]string{
		"repos/" + full: `{"default_branch":"main","allow_squash_merge":true,"allow_merge_commit":false,"allow_rebase_merge":false,"delete_branch_on_merge":true}`,
		"repos/" + full + "/branches/main/protection": `{"required_status_checks":{"strict":false,"contexts":["check"]},"enforce_admins":{"enabled":true}}`,
		"repos/" + full + "/labels?per_page=100":      `[{"name":"codex-review"},{"name":"bug"}]`,
	}
}

// allFleetResponses maps every roster repo to the same canned answers.
func allFleetResponses(shape func(full string) map[string]string) map[string]string {
	responses := map[string]string{}
	for _, name := range []string{"ferret", "snipe", "trixi-bot", "canapay", "fo", "loto", "strand", "npharvester", "trixi", "atomicfile", "keyring", "next", "conform-to-sdlc"} {
		maps.Copy(responses, shape("dkoosis/"+name))
	}
	return responses
}

// TestRunFleet_ConformingFleet: every roster repo fully configured = zero
// findings.
func TestRunFleet_ConformingFleet(t *testing.T) {
	restore := checks.SetGHAPI(ghStub(allFleetResponses(conformingRepoResponses)))
	defer restore()

	findings, err := checks.RunFleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("conforming fleet produced findings:\n%v", findings)
	}
}

// TestRunFleet_StockRepo: one repo left on GitHub's defaults trips the three
// settings rules, and every finding names the repo and carries a gh repair.
func TestRunFleet_StockRepo(t *testing.T) {
	responses := allFleetResponses(conformingRepoResponses)
	// snipe goes stock: no protection, stock labels, everything-on merges.
	delete(responses, "repos/dkoosis/snipe/branches/main/protection")
	responses["repos/dkoosis/snipe"] = `{"default_branch":"main","allow_squash_merge":true,"allow_merge_commit":true,"allow_rebase_merge":true,"delete_branch_on_merge":false}`
	responses["repos/dkoosis/snipe/labels?per_page=100"] = `[{"name":"bug"},{"name":"enhancement"}]`
	restore := checks.SetGHAPI(ghStub(responses))
	defer restore()

	findings, err := checks.RunFleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	rules := map[string]bool{}
	for _, f := range findings {
		if f.File != "dkoosis/snipe" {
			t.Errorf("finding for unexpected repo %s: %s", f.File, f.Rule)
			continue
		}
		rules[f.Rule] = true
		if f.Repair == "" {
			t.Errorf("%s finding carries no repair", f.Rule)
		}
	}
	for _, want := range []string{checks.RuleBranchProtection, checks.RuleFleetLabels, checks.RuleMergePolicy} {
		if !rules[want] {
			t.Errorf("stock repo did not trip %s (findings: %v)", want, findings)
		}
	}
}

// TestRunFleet_ProtectionShape: strict:true and a wrong context are each
// their own named drift.
func TestRunFleet_ProtectionShape(t *testing.T) {
	responses := allFleetResponses(conformingRepoResponses)
	responses["repos/dkoosis/trixi/branches/main/protection"] = `{"required_status_checks":{"strict":true,"contexts":["check (ubuntu-latest)"]},"enforce_admins":{"enabled":true}}`
	restore := checks.SetGHAPI(ghStub(responses))
	defer restore()

	findings, err := checks.RunFleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var strictHit, contextHit bool
	for _, f := range findings {
		if f.File != "dkoosis/trixi" || f.Rule != checks.RuleBranchProtection {
			continue
		}
		if strings.Contains(f.Msg, "strict:true") {
			strictHit = true
		}
		if strings.Contains(f.Msg, `do not include "check"`) {
			contextHit = true
		}
	}
	if !strictHit || !contextHit {
		t.Errorf("want strict + context findings for trixi, got %v", findings)
	}
}

// TestRunFleet_ExceptionFromRepoValues: a repo's own conform.json exceptions
// apply to its fleet findings.
func TestRunFleet_ExceptionFromRepoValues(t *testing.T) {
	responses := allFleetResponses(conformingRepoResponses)
	responses["repos/dkoosis/keyring"] = `{"default_branch":"main","allow_squash_merge":true,"allow_merge_commit":false,"allow_rebase_merge":false,"delete_branch_on_merge":false}`
	valuesJSON := `{"profile": "lib", "exceptions": [{"rule": "merge-policy", "reason": "finished micro-lib; branch cleanup is manual by choice"}]}`
	responses["repos/dkoosis/keyring/contents/docs/conform.json"] = fmt.Sprintf(`{"content":%q}`, base64.StdEncoding.EncodeToString([]byte(valuesJSON)))
	restore := checks.SetGHAPI(ghStub(responses))
	defer restore()

	findings, err := checks.RunFleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.File == "dkoosis/keyring" && f.Rule == checks.RuleMergePolicy {
			t.Errorf("excepted merge-policy still reported: %v", f)
		}
	}
}
