package checks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dkoosis/repo-standard/internal/values"
)

// Surface 3 (--fleet): GitHub-side settings, swept over every repo in the
// embedded roster via the gh CLI (keychain auth). No standing runner — GH
// settings drift only on deliberate change, so this runs at promulgation,
// pin-bump sweeps, and on demand. Findings name repo, rule, and the gh call
// that fixes it.
const (
	// RuleBranchProtection — main requires the `check` status, strict:false
	// (parallel agent PRs make strict a re-run tax — deliberate amendment of
	// the earlier pattern), enforce_admins on.
	RuleBranchProtection = "branch-protection"
	// RuleFleetLabels — the fleet label set present. Floor is codex-review
	// (the on-demand review trigger); D9 expansion lands in fleetLabels.
	RuleFleetLabels = "fleet-labels"
	// RuleMergePolicy — squash-only + delete-branch-on-merge, chosen, not
	// GitHub's everything-on default.
	RuleMergePolicy = "merge-policy"
)

const fleetOwner = "dkoosis"

// fleetLabels is the label floor every fleet repo carries.
var fleetLabels = []string{"codex-review"}

// requiredCheckContext is the status context branch protection must require —
// the `check` job of the check workflow, same name fleet-wide.
const requiredCheckContext = "check"

const ghTimeout = 15 * time.Second

// ghAPI fetches one GitHub API path via the gh CLI. Swappable for tests.
var ghAPI = func(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", path)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "HTTP 404") {
			return nil, errNotFound
		}
		return nil, fmt.Errorf("gh api %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return []byte(stdout.String()), nil
}

var (
	errNotFound      = errors.New("not found")
	errGHUnavailable = errors.New("gh not on PATH — the fleet surface authenticates through the gh keychain")
)

// RunFleet sweeps every roster repo concurrently and returns the combined
// findings, each repo's own repo-standard.json exceptions applied.
func RunFleet(ctx context.Context) ([]Finding, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, errGHUnavailable
	}
	fleet, err := values.DefaultFleet()
	if err != nil {
		return nil, fmt.Errorf("embedded roster: %w", err)
	}

	results := make([][]Finding, len(fleet.Repos))
	sem := make(chan struct{}, 4) // a few repos in flight; each is ~5 sequential GETs
	var wg sync.WaitGroup
	for i, repo := range fleet.Repos {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = checkFleetRepo(ctx, repo.Name)
		})
	}
	wg.Wait()

	var findings []Finding
	for _, r := range results {
		findings = append(findings, r...)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings, nil
}

// repoSettings is the subset of GET /repos/{owner}/{repo} the sweep reads.
type repoSettings struct {
	DefaultBranch       string `json:"default_branch"`
	AllowSquashMerge    bool   `json:"allow_squash_merge"`
	AllowMergeCommit    bool   `json:"allow_merge_commit"`
	AllowRebaseMerge    bool   `json:"allow_rebase_merge"`
	DeleteBranchOnMerge bool   `json:"delete_branch_on_merge"`
}

// protection is the subset of the branch-protection response the sweep reads.
type protection struct {
	RequiredStatusChecks *struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
	} `json:"required_status_checks"`
	EnforceAdmins struct {
		Enabled bool `json:"enabled"`
	} `json:"enforce_admins"`
}

// checkFleetRepo runs the four settings checks against one repo.
func checkFleetRepo(ctx context.Context, name string) []Finding {
	full := fleetOwner + "/" + name
	add := func(findings []Finding, rule, msg, repair string) []Finding {
		return append(findings, Finding{File: full, Rule: rule, Msg: msg, Repair: repair})
	}

	settings, err := fetchJSON[repoSettings](ctx, "repos/"+full)
	if err != nil {
		return add(nil, RuleBranchProtection,
			fmt.Sprintf("repo unreadable (%v) — sweep cannot see it", err),
			"gh auth status; check the roster entry in internal/values/fleet.json")
	}
	branch := settings.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	findings := protectionFindings(ctx, full, branch)
	findings = append(findings, labelFindings(ctx, full)...)
	findings = append(findings, mergePolicyFindings(full, settings)...)

	return applyExceptions(findings, fleetValues(ctx, full))
}

func protectionFindings(ctx context.Context, full, branch string) []Finding {
	repairPut := fmt.Sprintf(
		`printf '{"required_status_checks":{"strict":false,"contexts":["check"]},"enforce_admins":true,"required_pull_request_reviews":null,"restrictions":null}' | gh api -X PUT repos/%s/branches/%s/protection --input -`,
		full, branch)

	prot, err := fetchJSON[protection](ctx, "repos/"+full+"/branches/"+branch+"/protection")
	if errors.Is(err, errNotFound) {
		return []Finding{{File: full, Rule: RuleBranchProtection,
			Msg:    branch + " is unprotected — PRs merge with no required checks",
			Repair: repairPut}}
	}
	if err != nil {
		return []Finding{{File: full, Rule: RuleBranchProtection,
			Msg:    fmt.Sprintf("protection unreadable: %v", err),
			Repair: "gh auth status"}}
	}

	var findings []Finding
	switch rsc := prot.RequiredStatusChecks; rsc {
	case nil:
		findings = append(findings, Finding{File: full, Rule: RuleBranchProtection,
			Msg: "no required status checks — the check gate is not required to merge", Repair: repairPut})
	default:
		if !slices.Contains(rsc.Contexts, requiredCheckContext) {
			findings = append(findings, Finding{File: full, Rule: RuleBranchProtection,
				Msg:    fmt.Sprintf("required contexts %v do not include %q", rsc.Contexts, requiredCheckContext),
				Repair: repairPut})
		}
		if rsc.Strict {
			findings = append(findings, Finding{File: full, Rule: RuleBranchProtection,
				Msg:    "strict:true — parallel agent PRs pay a re-run tax on every merge (fleet amendment: strict:false)",
				Repair: repairPut})
		}
	}
	if !prot.EnforceAdmins.Enabled {
		findings = append(findings, Finding{File: full, Rule: RuleBranchProtection,
			Msg: "enforce_admins off — the gate has an owner-shaped hole", Repair: repairPut})
	}
	return findings
}

func labelFindings(ctx context.Context, full string) []Finding {
	type label struct {
		Name string `json:"name"`
	}
	labelsPtr, err := fetchJSON[[]label](ctx, "repos/"+full+"/labels?per_page=100")
	if err != nil {
		return []Finding{{File: full, Rule: RuleFleetLabels,
			Msg: fmt.Sprintf("labels unreadable: %v", err), Repair: "gh auth status"}}
	}
	labels := *labelsPtr
	have := make(map[string]bool, len(labels))
	for _, l := range labels {
		have[l.Name] = true
	}
	var findings []Finding
	for _, want := range fleetLabels {
		if !have[want] {
			findings = append(findings, Finding{File: full, Rule: RuleFleetLabels,
				Msg:    fmt.Sprintf("label %q missing — fleet machinery that targets it silently no-ops here", want),
				Repair: fmt.Sprintf("gh label create %s -R %s --color 5319e7 --description 'on-demand codex review'", want, full)})
		}
	}
	return findings
}

func mergePolicyFindings(full string, s *repoSettings) []Finding {
	repair := fmt.Sprintf("gh api -X PATCH repos/%s -F allow_squash_merge=true -F allow_merge_commit=false -F allow_rebase_merge=false -F delete_branch_on_merge=true", full)
	var wrong []string
	if !s.AllowSquashMerge {
		wrong = append(wrong, "squash disabled")
	}
	if s.AllowMergeCommit {
		wrong = append(wrong, "merge commits allowed")
	}
	if s.AllowRebaseMerge {
		wrong = append(wrong, "rebase merges allowed")
	}
	if !s.DeleteBranchOnMerge {
		wrong = append(wrong, "merged branches kept")
	}
	if len(wrong) == 0 {
		return nil
	}
	return []Finding{{File: full, Rule: RuleMergePolicy,
		Msg:    "merge policy is GitHub's accidental default, not the fleet choice (squash-only + delete-branch-on-merge): " + strings.Join(wrong, ", "),
		Repair: repair}}
}

// fleetValues fetches a repo's docs/repo-standard.json so its declared exceptions apply
// to fleet findings too, falling back to the root copy (LegacyValuesFile) while
// the fleet migrates. Missing or invalid → no exceptions (Surface 1 of
// that repo owns the values-file finding).
//
// The fallback has to be here as well as in loadValues: a fleet sweep reads
// GitHub, never the working tree, so a local-only fallback would leave every
// unmigrated repo exception-less in exactly the sweep that reports on all of
// them at once.
func fleetValues(ctx context.Context, full string) values.Values {
	none := values.Values{Profile: values.ProfileTool}
	data, err := ghAPI(ctx, "repos/"+full+"/contents/"+ValuesFile)
	if err != nil {
		data, err = ghAPI(ctx, "repos/"+full+"/contents/"+LegacyValuesFile)
	}
	if err != nil {
		return none
	}
	var contents struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(data, &contents) != nil {
		return none
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contents.Content, "\n", ""))
	if err != nil {
		return none
	}
	v, err := values.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return none
	}
	return *v
}

// fetchJSON GETs path and decodes into T.
func fetchJSON[T any](ctx context.Context, path string) (*T, error) {
	data, err := ghAPI(ctx, path)
	if err != nil {
		return nil, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("%s: bad JSON: %w", path, err)
	}
	return &v, nil
}
