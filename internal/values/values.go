package values

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Profile is a repo's SDLC surface. There are exactly two: the reference
// (full) surface and the lib carve-out. A third profile is not added
// speculatively — it earns its place only once an exception has appeared
// twice across the fleet (design call, not enforced here).
type Profile string

const (
	// ProfileTool is the full SDLC surface: check + local + fleet, deploy
	// included, beads required.
	ProfileTool Profile = "tool"
	// ProfileLib is the tool contract minus the deploy verb; beads optional.
	// A lib repo carries no "no-deploy" exception — no-deploy is what the
	// profile means, not something it opts out of.
	ProfileLib Profile = "lib"
)

// Exception is one declared, reasoned departure from the fleet contract.
// Every exception carries a reason; there is no bare "off" switch.
//
// A lint-set exception may instead name the linters it drops, each with its
// own reason: {"rule": "lint-set", "drop": {"dupl": "<reason>"}}. It then
// excuses only those linters, not the whole rule, and needs no top-level
// reason. The checks package reports a drop whose reason is empty.
type Exception struct {
	Rule   string            `json:"rule"`
	Reason string            `json:"reason,omitempty"`
	Drop   map[string]string `json:"drop,omitempty"`
	// Allow is the deadcode-filter exception: each function the repo's
	// deadcode target may filter out of deadcode's output, with a reason.
	Allow map[string]string `json:"allow,omitempty"`
}

// DropRule is the one rule id an exception may carry a drop for.
const DropRule = "lint-set"

// FilterRule is the one rule id an exception may carry an allow list for, and
// the only way to excuse it: a plain exception on it is invalid.
const FilterRule = "deadcode-filter"

// Values is one repo's repo-standard.json: its profile plus its declared
// exceptions.
type Values struct {
	Profile    Profile     `json:"profile"`
	Exceptions []Exception `json:"exceptions"`
}

var (
	// ErrInvalidProfile marks a profile field that is neither "tool" nor
	// "lib" (including empty/missing).
	ErrInvalidProfile = errors.New(`profile must be "tool" or "lib"`)
	// ErrEmptyRule marks an exception with an empty rule id.
	ErrEmptyRule = errors.New("exception rule must not be empty")
	// ErrEmptyReason marks an exception with an empty reason.
	ErrEmptyReason = errors.New("exception reason must not be empty")
	// ErrDropRule marks a drop on a rule other than DropRule.
	ErrDropRule = errors.New(`only a "lint-set" exception may carry a drop`)
	// ErrAllowRule marks an allow on a rule other than FilterRule, or a
	// FilterRule exception with no allow list.
	ErrAllowRule = errors.New(`only a "deadcode-filter" exception carries allow, and it must carry one: {"rule": "deadcode-filter", "allow": {"<function>": "<reason>"}}`)
	// ErrAllowReason marks an allowed function with an empty reason.
	ErrAllowReason = errors.New("every allowed function needs a reason")
	// ErrDuplicateRule marks two exceptions in the same file declaring the
	// same rule id.
	ErrDuplicateRule = errors.New("duplicate exception rule")
	// ErrTrailingData marks non-whitespace content after the top-level JSON
	// object — a sign the file holds more than one document.
	ErrTrailingData = errors.New("trailing data after JSON object")
)

// strictDecode decodes exactly one JSON value from r into v, hard-failing on
// unknown fields and on any data left over after that value. Shared by
// Values and Fleet decoding — both schemas are closed the same way.
func strictDecode(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ErrTrailingData
	}

	return nil
}

// Parse decodes and validates a Values document from r. Unknown fields at
// any level and trailing data after the top-level object are hard errors —
// there is no soft-fail path anywhere in this package.
func Parse(r io.Reader) (*Values, error) {
	var v Values
	if err := strictDecode(r, &v); err != nil {
		return nil, err
	}

	if err := v.validate(); err != nil {
		return nil, err
	}

	return &v, nil
}

// Load reads and parses the values file at path, wrapping every error
// (open, decode, or validate) with the path so a failure is actionable
// without extra context.
func Load(path string) (*Values, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	defer f.Close()

	v, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	return v, nil
}

// validate checks profile membership and per-exception invariants: every
// exception names a non-empty rule and reason, and no rule id repeats.
func (v *Values) validate() error {
	if v.Profile != ProfileTool && v.Profile != ProfileLib {
		return fmt.Errorf("%w: got %q", ErrInvalidProfile, v.Profile)
	}

	seen := make(map[string]bool, len(v.Exceptions))

	for i, exc := range v.Exceptions {
		if exc.Rule == "" {
			return fmt.Errorf("exception %d: %w", i, ErrEmptyRule)
		}

		if len(exc.Drop) > 0 && exc.Rule != DropRule {
			return fmt.Errorf("exception %d (rule %q): %w", i, exc.Rule, ErrDropRule)
		}

		if (len(exc.Allow) > 0) != (exc.Rule == FilterRule) {
			return fmt.Errorf("exception %d (rule %q): %w", i, exc.Rule, ErrAllowRule)
		}

		for fn, why := range exc.Allow {
			if strings.TrimSpace(fn) == "" || strings.TrimSpace(why) == "" {
				return fmt.Errorf("exception %d (rule %q, function %q): %w", i, exc.Rule, fn, ErrAllowReason)
			}
		}

		if exc.Reason == "" && len(exc.Drop) == 0 && len(exc.Allow) == 0 {
			return fmt.Errorf("exception %d (rule %q): %w", i, exc.Rule, ErrEmptyReason)
		}

		if seen[exc.Rule] {
			return fmt.Errorf("exception %d (rule %q): %w", i, exc.Rule, ErrDuplicateRule)
		}

		seen[exc.Rule] = true
	}

	return nil
}
