package values_test

import (
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

	if _, ok := byName["repo-standard"]; !ok {
		t.Error("fleet roster does not contain repo-standard")
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

	const wantPublic, wantPrivate = 8, 3
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
