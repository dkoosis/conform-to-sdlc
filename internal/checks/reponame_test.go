package checks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// TestReadme_LeadingHTMLCommentStillPasses: a comment renders as nothing, so
// like a blank line it must not stand between the page and its heading.
// trixi's KG-publish banner sits on line 1 by its own contract.
func TestReadme_LeadingHTMLCommentStillPasses(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[checks.ReadmeFile] = "<!-- auto-published from KG -->\n# trixi\n\nprose.\n"
	dir := writeRepo(t, files)
	if got := checks.CheckReadme(dir); len(got) != 0 {
		t.Fatalf("leading HTML comment: want no findings, got %+v", got)
	}
}

// TestReadme_CommentThenProseIsStillAFinding: skipping the comment must not
// skip the requirement.
func TestReadme_CommentThenProseIsStillAFinding(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[checks.ReadmeFile] = "<!-- banner -->\nprose with no heading.\n"
	dir := writeRepo(t, files)
	if got := checks.CheckReadme(dir); len(got) != 1 {
		t.Fatalf("comment then prose: want one finding, got %+v", got)
	}
}

// TestFix_NamesTheRepoFromGoModNotTheDirectory: --fix run inside a worktree
// titled trixi's page "# conform-rename" after the worktree's directory.
func TestFix_NamesTheRepoFromGoModNotTheDirectory(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	delete(files, checks.ReadmeFile)
	files["go.mod"] = "module github.com/dkoosis/widget\n\ngo 1.26\n"
	dir := writeRepo(t, files)
	wt := filepath.Join(filepath.Dir(dir), "some-worktree-name")
	if err := os.Rename(dir, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := checks.Fix(wt); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(wt, checks.ReadmeFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "some-worktree-name") || !strings.Contains(string(body), "widget") {
		t.Errorf("%s names the directory, not the module:\n%s", checks.ReadmeFile, body)
	}
}
