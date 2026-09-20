package checks

import (
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// A rename is the one edit the Go compiler half-catches. It fixes every Go
// caller and sees nothing else: the docs, shell hooks, rule files and test
// names that spell the old word keep spelling it, and the breakage surfaces
// days later, one reference at a time, in review. mnemd's reindex->index
// rename left five such references across three PRs over four days, and one
// of them was a hook that silently stopped indexing (cfm-531 is the other
// half of that incident).
//
// The rename is visible in the diff, so this rule reads the diff, not the
// tree. A branch that renamed nothing removes no declaration and the rule
// returns before it touches the filesystem — the property that keeps it from
// becoming ceremony on every `make check`.
//
// The load-bearing test is not "was this a rename" but "is this word still
// live Go". Live means one of two positions the compiler actually reads: a
// declaration, or an exact quoted literal. Everything else a name can occupy
// in a .go file — a comment, a test function name, a diagnostic string — is
// prose that happens to live in Go, and it goes stale exactly like a README.
//
// Reading "live" as "appears anywhere in a .go file" was the first shape of
// this rule and it caught nothing. Replayed against mnemd's own reindex->index
// commit it found zero of the five known survivors, because `reindex` still
// sat in comments and test names across ten Go files: the stale Go text
// vouched for the stale prose. Two consequences, and the rule needs both.
// Live positions are matched, not the bare word. And the sweep covers .go
// files too — two of those five survivors were Go test names.
//
// What the live test buys, once it is right: a moved declaration, a dropped
// overload and a same-diff re-add all leave the name in a live position, so
// all three stay quiet without a heuristic for any of them.

// minNameLen is the shortest token worth sweeping. Below it, a name carries
// too little signal to distinguish a rename from a coincidence in prose, and
// the "absent from all Go" test is doing the filtering alone.
const minNameLen = 4

// declPatterns match a Go declaration on a removed diff line. Declaration
// position is what makes the token a name rather than a word: an identifier
// deleted from the middle of an expression is a call site the compiler
// already handled.
var declPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^-\s*func\s+\(\s*[^)]*\)\s*([A-Za-z_]\w*)`), // method
	regexp.MustCompile(`^-\s*func\s+([A-Za-z_]\w*)`),                // function
	regexp.MustCompile(`^-\s*(?:type|const|var)\s+([A-Za-z_]\w*)`),  // type/const/var
	regexp.MustCompile(`^-\s*([A-Za-z_]\w*)\s+[\w*\[\].]+\s*=`),     // typed member of a const/var block
	regexp.MustCompile(`^-\s*([A-Za-z_]\w*)\s*=\s`),                 // untyped member of a const/var block
}

// literalPattern matches a quoted bare word on a removed line. CLI verbs,
// subcommands and flag names live in tables as string literals, so a renamed
// verb never appears in declaration position. The lowercase-and-dashes shape
// is what a command name looks like; anything else is prose in a string.
//
// Literals are read only from removed lines in non-test Go, and that
// restriction is load-bearing. Read from the whole diff, this pattern lifts
// every quoted word a test fixture deletes — the mnemd replay produced
// findings for `grebes`, `cormorants` and `bogus`, all of them fixture nouns
// from a table-driven test. A program's vocabulary is declared in its source;
// a word only a test says is that test's own.
var literalPattern = regexp.MustCompile(`"([a-z][a-z0-9_-]*)"`)

// fileHeader marks the start of a per-file section of a unified diff.
var fileHeader = regexp.MustCompile(`^\+\+\+ b/(.*)$`)

// isSourceGo reports whether a diff section's file is Go the program ships.
func isSourceGo(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

// checkStaleNames reports every reference, outside Go, to a name this branch
// removed from Go.
func checkStaleNames(dir string, keep map[string]bool) []Finding {
	diff, ok := renameDiff(dir)
	if !ok || diff == "" {
		return nil
	}

	decls, verbs := removedNames(diff)
	if len(decls) == 0 && len(verbs) == 0 {
		return nil // nothing was renamed; no filesystem work
	}

	var findings []Finding
	for _, name := range append(decls, verbs...) {
		if keep[name] {
			continue
		}
		if stillLive(dir, name, contains(verbs, name)) {
			continue // still declared, or still in the verb table: not a rename
		}
		for _, hit := range strings.Split(strings.TrimRight(gitGrep(dir, name), "\n"), "\n") {
			if hit == "" {
				continue
			}
			file, line := splitGrepHit(hit)
			findings = append(findings, Finding{
				File: file,
				Rule: RuleStaleName,
				Msg: "line " + line + " still says " + name + ", which this branch removed from Go — " +
					"the compiler fixed the Go callers and cannot see this one",
				Repair: "update the reference, or declare it in " + ValuesFile +
					` under keepNames with a reason if the old word must survive here`,
			})
		}
	}
	return findings
}

// renameDiff returns the diff that could contain a rename: the branch's whole
// divergence from main plus anything uncommitted. Diffing against the
// merge-base with no second revision compares that commit to the working
// tree, so committed branch work and an in-progress edit arrive together —
// the rule fires while the rename is still cheap to finish.
//
// Off a branch there is no divergence to read and only uncommitted work can
// hold a fresh rename. A repo with no git, or no main, yields no diff and the
// rule stands down; it is a net over renames, not a gate on repo shape.
func renameDiff(dir string) (string, bool) {
	if base, ok := gitOut(dir, "merge-base", "main", "HEAD"); ok {
		if base = strings.TrimSpace(base); base != "" {
			head, _ := gitOut(dir, "rev-parse", "HEAD")
			if base != strings.TrimSpace(head) {
				return gitOut(dir, "diff", "--unified=0", base)
			}
		}
	}
	return gitOut(dir, "diff", "--unified=0", "HEAD")
}

// removedNames collects, from the diff's removed lines in source Go, the
// names declared there and the command words quoted there — kept apart
// because each is checked against its own namespace. Both come back sorted
// and deduplicated so the findings hold a stable order.
func removedNames(diff string) (decls, verbs []string) {
	seenDecl := map[string]bool{}
	seenVerb := map[string]bool{}
	inSource := true

	for _, line := range strings.Split(diff, "\n") {
		if m := fileHeader.FindStringSubmatch(line); m != nil {
			inSource = isSourceGo(m[1])
			continue
		}
		if !inSource || !strings.HasPrefix(line, "-") || strings.HasPrefix(line, "---") {
			continue
		}
		for _, re := range declPatterns {
			if m := re.FindStringSubmatch(line); m != nil {
				if len(m[1]) >= minNameLen {
					seenDecl[m[1]] = true
				}
				break
			}
		}
		for _, m := range literalPattern.FindAllStringSubmatch(line, -1) {
			if len(m[1]) >= minNameLen {
				seenVerb[m[1]] = true
			}
		}
	}

	return sortedKeys(seenDecl), sortedKeys(seenVerb)
}

// sortedKeys returns a set's members in a stable order.
func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}

	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// contains reports whether needle is in haystack.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}

	return false
}

// liveInGo reports whether name still occupies a position the Go compiler
// reads: declared as a func, method, type, const or var, or written as an
// exact quoted literal. A match in a comment, a test function name or a
// longer diagnostic string is not live — that is the distinction the rule
// exists to draw.
//
// A declared name and a command verb get SEPARATE live tests, and that is the
// third thing the mnemd replay taught. They look like one question and are
// two: mnemd retired the verb `reindex` while keeping `func reindex` and
// `nugbase.Reindex`, so a single test that consults Go declarations lets the
// surviving function vouch for the dead verb, and every doc still saying
// `mnemd reindex` passes. A verb lives in the table the program reads, a
// declared name lives in its declaration, and each dies in its own namespace
// while the other goes on.
//
// Test files are excluded, and the reason is the second thing the mnemd
// replay taught. At the reindex->index commit the one surviving live literal
// "reindex" sat in a test asserting that the retired verbs are REJECTED — a
// test whose whole job is to name dead words, and which would therefore vouch
// for every one of them forever. A name's live home is the declaration or the
// table the program itself reads; a test that mentions it is a caller, and a
// name that only tests declare has no live home worth protecting.
func stillLive(dir, name string, isVerb bool) bool {
	// A verb's one live home is the table the program reads, so it is asked
	// for only as an exact literal. Consulting declarations here is what let
	// `func reindex` keep the retired verb `reindex` alive.
	if isVerb {
		out, _ := gitOut(dir, "grep", "-lE", "--", `"`+regexp.QuoteMeta(name)+`"`, "--", "*.go", ":!*_test.go")
		return out != ""
	}

	// git grep's POSIX regex has no \\b, and a pattern that uses one matches
	// nothing without saying so — this check was silently dead for exactly
	// that reason until the mnemd replay showed a live `var verbs` reading
	// as removed. The boundary is spelled out instead.
	q := regexp.QuoteMeta(name)
	end := `([^[:alnum:]_]|$)`
	live := strings.Join([]string{
		`(func|type|const|var)[[:space:]]+` + q + end, // declaration
		`\)[[:space:]]*` + q + `\(`,                   // method on a receiver
		`^[[:space:]]*` + q + `[[:space:]]+[^=]*=`,    // member of a const/var block
		`"` + q + `"`, // exact literal: a CLI verb or flag
	}, "|")

	out, _ := gitOut(dir, "grep", "-lE", "--", live, "--", "*.go", ":!*_test.go")

	return out != ""
}

// gitGrep returns git grep's output for a whole-word search over every
// tracked file, or "" for no match. Whole-word, not substring: sweeping
// `adopt` as a substring reports `adoption`, `adoptOne` and `adopt_test.go`,
// none of which is a reference to the removed name.
func gitGrep(dir, needle string) string {
	out, _ := gitOut(dir, "grep", "-nwF", "--", needle)
	return out
}

// splitGrepHit splits a `path:line:text` hit into its path and line number.
func splitGrepHit(hit string) (file, line string) {
	parts := strings.SplitN(hit, ":", 3)
	if len(parts) < 2 {
		return hit, "?"
	}
	return parts[0], parts[1]
}

// gitOut runs git in dir and returns its stdout. Exit status 1 is git's
// "nothing matched" for grep and its "no such commit" for merge-base, both of
// which are answers rather than failures, so they come back as empty output
// with ok true; anything else stands the rule down.
func gitOut(dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return "", true
		}
		return "", false
	}
	return string(out), true
}
