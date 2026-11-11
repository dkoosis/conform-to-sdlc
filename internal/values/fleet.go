package values

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
)

// Visibility is a repo's GitHub visibility: the public/private split the
// fleet roster tracks and nothing else.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// Repo is one fleet-level fact: a repo's name and visibility. Profile and
// exceptions are NOT here — those stay per-repo, declared in each repo's own
// repo-standard.json. No fact has two homes.
type Repo struct {
	Name       string     `json:"name"`
	Visibility Visibility `json:"visibility"`
}

// Fleet is the roster of every repo repo-standard's --fleet surface checks.
type Fleet struct {
	Repos []Repo `json:"repos"`
}

var (
	// ErrEmptyRepoName marks a roster entry with an empty name.
	ErrEmptyRepoName = errors.New("repo name must not be empty")
	// ErrInvalidVisibility marks a roster entry whose visibility is neither
	// "public" nor "private".
	ErrInvalidVisibility = errors.New(`visibility must be "public" or "private"`)
	// ErrDuplicateRepo marks two roster entries declaring the same repo
	// name.
	ErrDuplicateRepo = errors.New("duplicate repo name")
)

//go:embed fleet.json
var fleetJSON embed.FS

// DefaultFleet loads and validates the fleet roster embedded in the repo-standard
// binary, so `repo-standard --fleet` runs without a checkout of every repo.
func DefaultFleet() (*Fleet, error) {
	data, err := fleetJSON.ReadFile("fleet.json")
	if err != nil {
		return nil, fmt.Errorf("load embedded fleet.json: %w", err)
	}

	var f Fleet
	if err := strictDecode(bytes.NewReader(data), &f); err != nil {
		return nil, fmt.Errorf("load embedded fleet.json: %w", err)
	}

	if err := f.validate(); err != nil {
		return nil, fmt.Errorf("load embedded fleet.json: %w", err)
	}

	return &f, nil
}

// validate checks that every repo has a non-empty name, a valid visibility,
// and that no name repeats.
func (f *Fleet) validate() error {
	seen := make(map[string]bool, len(f.Repos))

	for i, repo := range f.Repos {
		if repo.Name == "" {
			return fmt.Errorf("repo %d: %w", i, ErrEmptyRepoName)
		}

		if repo.Visibility != VisibilityPublic && repo.Visibility != VisibilityPrivate {
			return fmt.Errorf("repo %d (%q): %w: got %q", i, repo.Name, ErrInvalidVisibility, repo.Visibility)
		}

		if seen[repo.Name] {
			return fmt.Errorf("repo %d (%q): %w", i, repo.Name, ErrDuplicateRepo)
		}

		seen[repo.Name] = true
	}

	return nil
}
