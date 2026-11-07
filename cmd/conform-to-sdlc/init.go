package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
	"github.com/dkoosis/conform-to-sdlc/internal/values"
)

// runInit scaffolds a new repo and wires it up.
//
// The skeleton comes from the checker's own renderer (internal/checks), so a
// scaffolded repo passes `conform-to-sdlc` unedited — that shared renderer is the
// reason this exists as a built command rather than a copier template
// (decision conform-to-sdlc-init-build-thin).
//
// Remote state is opt-in. --with-remote is the only path that runs a gh
// command; without it the GitHub steps are printed for the operator.
// initFlags is one parsed `conform-to-sdlc init` invocation.
type initFlags struct {
	spec       checks.ScaffoldSpec
	target     string
	filesOnly  bool
	dryRun     bool
	withRemote bool
}

// parseInitFlags reads the command line into an initFlags, printing usage on
// anything it cannot accept.
func parseInitFlags(args []string) (initFlags, error) {
	fs := flag.NewFlagSet("conform-to-sdlc init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		dir        = fs.String("dir", "", "target directory (default: ./<repo>)")
		owner      = fs.String("owner", "", "GitHub owner (default: the fleet owner)")
		module     = fs.String("module", "", "go module path (default: github.com/<owner>/<repo>)")
		prefix     = fs.String("prefix", "", "bd issue prefix, 2-3 letters (default: first three letters of <repo>)")
		planDir    = fs.String("plan-dir", "", "bd custom.plan_dir")
		profile    = fs.String("profile", string(values.ProfileTool), "conform-to-sdlc profile: tool | lib")
		lintPin    = fs.String("lint-pin", "", "golangci-lint version for the single pin file")
		filesOnly  = fs.Bool("files-only", false, "emit the skeleton and skip machine bootstrap entirely")
		dryRun     = fs.Bool("dry-run", false, "print what would be written and run; change nothing")
		withRemote = fs.Bool("with-remote", false, "ALSO run the GitHub steps (labels, merge policy, branch protection) against a real account")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: conform-to-sdlc init <repo> [flags]

Scaffolds a repo that passes `+"`conform-to-sdlc`"+` unedited, then wires the machine
half (git init, bd init, bd config). GitHub state is NOT touched unless
--with-remote is passed.

flags:
`)
		fs.PrintDefaults()
	}
	// Go's flag package stops at the first non-flag argument, so pull the
	// repo name out first — `conform-to-sdlc init widget --dry-run` must work in the
	// order a human types it.
	repo, rest := splitPositional(args)
	if err := fs.Parse(rest); err != nil {
		return initFlags{}, errUsage
	}
	if repo == "" || fs.NArg() != 0 {
		fs.Usage()
		return initFlags{}, fmt.Errorf("%w: init takes exactly one repo name", errUsage)
	}

	out := initFlags{
		spec: checks.ScaffoldSpec{
			Repo:    repo,
			Owner:   *owner,
			Module:  *module,
			Prefix:  *prefix,
			PlanDir: *planDir,
			Profile: values.Profile(*profile),
			LintPin: *lintPin,
		},
		target:     *dir,
		filesOnly:  *filesOnly,
		dryRun:     *dryRun,
		withRemote: *withRemote,
	}
	if out.target == "" {
		out.target = filepath.Join(".", repo)
	}
	return out, nil
}

func runInit(ctx context.Context, args []string) error {
	opts, err := parseInitFlags(args)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return initDryRun(ctx, opts)
	}
	if err := writeSkeleton(opts); err != nil {
		return err
	}
	if !opts.filesOnly {
		bootstrap := checks.BootstrapOpts{WithRemote: opts.withRemote}
		if err := checks.Bootstrap(ctx, opts.target, opts.spec, bootstrap); err != nil {
			return err
		}
	}
	return reportInitResult(opts)
}

// initDryRun prints the skeleton and the bootstrap plan without writing or
// running anything.
func initDryRun(ctx context.Context, opts initFlags) error {
	fmt.Printf("conform-to-sdlc init %s → %s (dry run)\n", opts.spec.Repo, opts.target)
	for _, p := range checks.ScaffoldPaths(opts.spec) {
		fmt.Printf("  · would write: %s\n", p)
	}
	if opts.filesOnly {
		return nil
	}
	return checks.Bootstrap(ctx, opts.target, opts.spec, checks.BootstrapOpts{DryRun: true})
}

// writeSkeleton creates the target directory and emits the skeleton.
func writeSkeleton(opts initFlags) error {
	// The target is the operator's own CLI argument: init creates a directory
	// they named, and writes only the skeleton inside it.
	if err := os.MkdirAll(opts.target, 0o755); err != nil {
		return err
	}
	if err := checks.Scaffold(opts.target, opts.spec); err != nil {
		if errors.Is(err, checks.ErrScaffoldExists) {
			return fmt.Errorf("%w (use a fresh directory, or --dry-run to see the skeleton)", err)
		}
		return err
	}
	fmt.Printf("conform-to-sdlc init %s → %s\n", opts.spec.Repo, opts.target)
	for _, p := range checks.ScaffoldPaths(opts.spec) {
		fmt.Printf("  ✓ %s\n", p)
	}
	return nil
}

// reportInitResult proves the claim rather than printing it: the checker runs
// over what was just emitted, and any gap is reported as a finding of this
// run.
func reportInitResult(opts initFlags) error {
	findings := checks.Run(opts.target)
	if len(findings) > 0 {
		for _, f := range findings {
			fmt.Println(f)
		}
		return fmt.Errorf("%w: the scaffolded repo does not pass conform-to-sdlc — this is a conform-to-sdlc bug, not a repo problem", errFindings)
	}
	fmt.Println("conform-to-sdlc: ok (scaffolded repo passes unedited)")
	if !opts.withRemote && !opts.filesOnly {
		fmt.Println("note: GitHub state untouched — rerun with --with-remote once the repo exists on GitHub")
	}
	return nil
}

// splitPositional returns the first bare argument and everything else, in
// order. A repo name may appear before or after the flags.
func splitPositional(args []string) (string, []string) {
	var (
		positional string
		rest       []string
	)
	for i, a := range args {
		if positional == "" && !strings.HasPrefix(a, "-") && !takesValue(args, i) {
			positional = a
			continue
		}
		rest = append(rest, a)
	}
	return positional, rest
}

// takesValue reports whether args[i] is the value of the flag before it,
// written in the space-separated form (`-prefix wg`).
func takesValue(args []string, i int) bool {
	if i == 0 {
		return false
	}
	prev := args[i-1]
	if !strings.HasPrefix(prev, "-") || strings.Contains(prev, "=") {
		return false
	}
	return !boolFlags[strings.TrimLeft(prev, "-")]
}

// boolFlags are the init flags that take no value.
var boolFlags = map[string]bool{
	"files-only":  true,
	"dry-run":     true,
	"with-remote": true,
	"h":           true,
	"help":        true,
}
