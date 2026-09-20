package values

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
type Exception struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// KeptName is one name the stale-name rule must not sweep. A rename normally
// leaves nothing behind, so this list is empty in a repo that keeps no
// history in its prose; it exists for the file that must go on saying a dead
// word — a changelog, a migration note, an ADR recording what the word used
// to be.
//
// It carries a reason for the same purpose an Exception does: the reason is
// what a later reader needs to decide whether the entry is still earning its
// place. There is no bare name.
type KeptName struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Values is one repo's conform.json: its profile, its declared exceptions,
// and the names its prose may keep after Go drops them.
type Values struct {
	Profile    Profile     `json:"profile"`
	Exceptions []Exception `json:"exceptions"`
	KeepNames  []KeptName  `json:"keepNames,omitempty"`
}

// KeptNames indexes KeepNames for lookup by the stale-name rule.
func (v Values) KeptNames() map[string]bool {
	if len(v.KeepNames) == 0 {
		return nil
	}

	kept := make(map[string]bool, len(v.KeepNames))
	for _, k := range v.KeepNames {
		kept[k.Name] = true
	}

	return kept
}

var (
	// ErrInvalidProfile marks a profile field that is neither "tool" nor
	// "lib" (including empty/missing).
	ErrInvalidProfile = errors.New(`profile must be "tool" or "lib"`)
	// ErrEmptyRule marks an exception with an empty rule id.
	ErrEmptyRule = errors.New("exception rule must not be empty")
	// ErrEmptyReason marks an exception with an empty reason.
	ErrEmptyReason = errors.New("exception reason must not be empty")
	// ErrDuplicateRule marks two exceptions in the same file declaring the
	// same rule id.
	ErrDuplicateRule = errors.New("duplicate exception rule")
	// ErrEmptyName marks a keepNames entry with an empty name.
	ErrEmptyName = errors.New("keepNames entry must name a word")
	// ErrDuplicateName marks two keepNames entries holding the same name.
	ErrDuplicateName = errors.New("duplicate keepNames entry")
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
		return err
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

		if exc.Reason == "" {
			return fmt.Errorf("exception %d (rule %q): %w", i, exc.Rule, ErrEmptyReason)
		}

		if seen[exc.Rule] {
			return fmt.Errorf("exception %d (rule %q): %w", i, exc.Rule, ErrDuplicateRule)
		}

		seen[exc.Rule] = true
	}

	kept := make(map[string]bool, len(v.KeepNames))

	for i, k := range v.KeepNames {
		if k.Name == "" {
			return fmt.Errorf("keepNames %d: %w", i, ErrEmptyName)
		}

		if k.Reason == "" {
			return fmt.Errorf("keepNames %d (name %q): %w", i, k.Name, ErrEmptyReason)
		}

		if kept[k.Name] {
			return fmt.Errorf("keepNames %d (name %q): %w", i, k.Name, ErrDuplicateName)
		}

		kept[k.Name] = true
	}

	return nil
}
