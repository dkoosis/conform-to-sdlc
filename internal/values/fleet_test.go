package values_test

import (
	"slices"
	"testing"

	"github.com/dkoosis/repo-standard/internal/values"
)

func TestDefaultFleet(t *testing.T) {
	t.Parallel()

	fleet, err := values.DefaultFleet()
	if err != nil {
		t.Fatalf("DefaultFleet() returned error: %v", err)
	}

	byName := make(map[string]values.Repo, len(fleet.Repos))
	for _, repo := range fleet.Repos {
		byName[repo.Name] = repo
	}

	if _, ok := byName["ferret"]; !ok {
		t.Error("fleet roster does not contain ferret")
	}

	// The fleet of epic cfm-pxx: exactly these, in this order. repo-standard
	// gates itself through make check, not through the --fleet roster; sdlc,
	// a2a-cli and local-review are outside the Go contract.
	want := []string{"canapay", "mnemd", "loto", "ferret", "atomicfile", "beadwatch", "bill-cli", "fo", "itzy", "keyring", "snipe", "strand", "trixi", "upcheck"}
	got := make([]string, 0, len(fleet.Repos))
	for _, repo := range fleet.Repos {
		got = append(got, repo.Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("fleet roster = %v, want exactly %v", got, want)
	}

	if _, ok := byName["cc-plugins"]; ok {
		t.Error("fleet roster contains cc-plugins, want it absent (markdown-only, outside the Go contract)")
	}

	seen := make(map[string]bool, len(fleet.Repos))

	var publicCount, privateCount int

	for _, repo := range fleet.Repos {
		if seen[repo.Name] {
			t.Errorf("fleet roster has duplicate repo name %q", repo.Name)
		}

		seen[repo.Name] = true

		switch repo.Visibility {
		case values.VisibilityPublic:
			publicCount++
		case values.VisibilityPrivate:
			privateCount++
		default:
			t.Errorf("repo %q has invalid visibility %q", repo.Name, repo.Visibility)
		}
	}

	const wantPublic, wantPrivate = 8, 6
	if publicCount != wantPublic {
		t.Errorf("public repo count = %d, want %d", publicCount, wantPublic)
	}

	if privateCount != wantPrivate {
		t.Errorf("private repo count = %d, want %d", privateCount, wantPrivate)
	}

	if len(fleet.Repos) != wantPublic+wantPrivate {
		t.Errorf("total repo count = %d, want %d", len(fleet.Repos), wantPublic+wantPrivate)
	}
}
