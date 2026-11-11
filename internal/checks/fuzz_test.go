package checks_test

import (
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

const twoFuzz = `package x

import "testing"

func FuzzA(f *testing.F) { f.Fuzz(func(t *testing.T, s string) {}) }

func FuzzB(f *testing.F) { f.Fuzz(func(t *testing.T, s string) {}) }

// not fuzz functions: wrong parameter, a lowercase name
func FuzzNotF(t *testing.T) {}
func fuzzC(f *testing.F)    {}
`

func fuzzRepo(t *testing.T, fuzzRecipe string, tests map[string]string) []checks.Finding {
	t.Helper()
	files := map[string]string{"Makefile": withRecipe("fuzz", fuzzRecipe)}
	maps.Copy(files, tests)
	return checks.CheckFuzzCoverage(writeRepo(t, files))
}

func TestFuzzCoverage_NamesTheUnrunFunction(t *testing.T) {
	t.Parallel()
	got := fuzzRepo(t, "\tgo test -run '^$$' -fuzz '^FuzzA$$' -fuzztime=$(FUZZTIME) ./pkg\n", map[string]string{"pkg/a_test.go": twoFuzz})
	if len(got) != 1 || got[0].Rule != checks.RuleFuzzCoverage ||
		!strings.Contains(got[0].Msg, "FuzzB") || !strings.Contains(got[0].Msg, "pkg/a_test.go:7") {
		t.Fatalf("want one fuzz-coverage naming FuzzB and its file, got %v", got)
	}
}

func TestFuzzCoverage_NoFuzzRunFails(t *testing.T) {
	t.Parallel()
	one := "package x\nimport \"testing\"\nfunc FuzzOnly(f *testing.F) {}\n"
	for name, recipe := range map[string]string{
		"echo only":       "\t@echo fuzz\n",
		"no Fuzz message": "\t@echo \"no Fuzz functions\"\n",
		"commented -fuzz": "\t@echo skip # go test -fuzz '^FuzzOnly$$'\n",
	} {
		got := fuzzRepo(t, recipe, map[string]string{"a_test.go": one})
		if len(got) != 1 || !strings.Contains(got[0].Msg, "FuzzOnly") || !strings.Contains(got[0].Msg, "runs no `go test -fuzz`") {
			t.Errorf("%s: want one finding for the empty fuzz step, got %v", name, got)
		}
	}
}

func TestFuzzCoverage_Passes(t *testing.T) {
	t.Parallel()
	loop := "\t@for pkg in $$(go list ./...); do for f in $$(go test -list '^Fuzz' $$pkg | sed -n '/^Fuzz/p'); do go test -run '^$$' -fuzz \"^$$f$$\" -fuzztime=$(FUZZTIME) $$pkg || exit 1; done; done\n"
	named := "\tgo test -run '^$$' -fuzz=^FuzzA$$ ./pkg\n\tgo test -run '^$$' -fuzz '^FuzzB$$' ./pkg\n"
	pattern := "\tgo test -run '^$$' -fuzz '^Fuzz[AB]$$' ./pkg\n"
	for name, recipe := range map[string]string{"list loop": loop, "each named": named, "regexp": pattern} {
		if got := fuzzRepo(t, recipe, map[string]string{"pkg/a_test.go": twoFuzz}); len(got) != 0 {
			t.Errorf("%s: produced %v", name, got)
		}
	}
	// No Fuzz function: an empty fuzz target passes.
	if got := fuzzRepo(t, "\t@echo \"no Fuzz functions\"\n", map[string]string{"a_test.go": "package x\n"}); len(got) != 0 {
		t.Errorf("repo with no Fuzz function produced %v", got)
	}
	// Recipes reached through a prerequisite count; vendored and testdata files do not.
	mk := strings.Replace(goodMakefile, "fuzz: ##", "fuzz: fuzz-a fuzz-b ##", 1) +
		"fuzz-a: ## a\n\tgo test -fuzz '^FuzzA$$' ./pkg\nfuzz-b: ## b\n\tgo test -fuzz '^FuzzB$$' ./pkg\n"
	dir := writeRepo(t, map[string]string{"Makefile": mk, "pkg/a_test.go": twoFuzz, "vendor/v/v_test.go": twoFuzz, "pkg/testdata/t_test.go": twoFuzz})
	if got := checks.CheckFuzzCoverage(dir); len(got) != 0 {
		t.Errorf("prerequisite recipes produced %v", got)
	}
}

// The rule runs no subprocess: the check stays under a second and offline.
func TestFuzzCoverage_NoSubprocess(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("fuzz.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "exec.Command") {
		t.Error("fuzz.go starts a subprocess")
	}
}

// No fuzz target at all: every Fuzz function is unrun (canapay's shape).
func TestFuzzCoverage_NoFuzzTarget(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, map[string]string{"Makefile": goodMakefile, "pkg/a_test.go": twoFuzz})
	mk, _ := os.ReadFile(dir + "/Makefile")
	if err := os.WriteFile(dir+"/Makefile", []byte(strings.Replace(string(mk), "fuzz: ## Fuzz\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := checks.CheckFuzzCoverage(dir)
	if len(got) != 2 || !strings.Contains(got[0].Msg, "no fuzz target") {
		t.Fatalf("want FuzzA and FuzzB unrun with no fuzz target, got %v", got)
	}
}
