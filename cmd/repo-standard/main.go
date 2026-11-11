// Command repo-standard checks a repo against the dkoosis fleet SDLC contract.
//
// Three surfaces, because the three kinds of state live in three places:
//
//	repo-standard          in-repo files (Makefile verbs, lint core, CI shape, bd config)
//	repo-standard --local  machine wiring CI can't see (hooksPath, hooks, dolt remote)
//	repo-standard --fleet  GitHub-side settings (protection, labels, merge policy)
//	repo-standard --fix    make the Surface-1 repairs that need no judgment, then check
//
// A fourth verb runs the contract backwards:
//
//	repo-standard init <repo>  scaffold a repo that passes the checks above unedited
//
// init emits the skeleton from the SAME renderer the checks read, so a rule
// change moves both halves at once.
//
// Two subcommands write rather than report:
//
//	repo-standard sandbox sync   rewrite .sandbox/lib from the canonical copy
//	repo-standard pack sync      rewrite .golangci-rules/ from the lintbrush pack
//
// Failures name file, rule, and repair command. There is no soft-fail: a rule
// too noisy to hard-fail gets deleted, not warned.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dkoosis/repo-standard/internal/checks"
	"github.com/dkoosis/repo-standard/internal/lintpack"
	"github.com/dkoosis/repo-standard/internal/sandbox"
)

// version is stamped at build time via -ldflags "-X main.version=<rev>"
// (see the Makefile's install target). A plain `go build` with no ldflags
// leaves it at this default.
var version = "unknown"

// errUsage marks a bad invocation; usage has already been printed.
var errUsage = errors.New("bad usage")

// errFindings marks a completed run that found contract violations — the
// hard-fail exit, distinct from repo-standard itself breaking.
var errFindings = errors.New("contract violations")

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, errFindings) {
			fmt.Fprintln(os.Stderr, "repo-standard:", err)
		}
		os.Exit(1)
	}
}

// parseMode reads argv[0] into a surface name. Three of the arguments are not
// modes at all — init runs the scaffolder, sandbox rewrites .sandbox/lib, and
// help prints usage — so it reports handled to tell run there is nothing left
// to check.
func parseMode(args []string) (mode string, handled bool, err error) {
	if len(args) == 0 {
		return "check", false, nil
	}
	switch args[0] {
	case "init":
		return "", true, runInit(context.Background(), args[1:])
	case "sandbox":
		return "", true, runSandbox(args[1:])
	case "pack":
		return "", true, runPack(args[1:])
	case "version":
		fmt.Println(version)
		return "", true, nil
	case "--local", "--fleet", "--fix":
		return args[0][2:], false, nil
	case "-h", "--help", "help":
		usage()
		return "", true, nil
	default:
		usage()
		return "", true, fmt.Errorf("%w: unknown argument %q", errUsage, args[0])
	}
}

func run(args []string) error {
	mode, handled, err := parseMode(args)
	if handled {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find the working directory: %w", err)
	}
	// --fix repairs first, then falls through to the ordinary check, so its
	// exit code still answers "is this repo conforming" rather than "did I
	// manage to write a file". A repo it cannot fully repair stays red, and a
	// repair that failed still lets the check name what is wrong.
	var fixErr error
	if mode == "fix" {
		var done []string
		done, fixErr = checks.Fix(dir)
		for _, line := range done {
			fmt.Println("repo-standard: fixed:", line)
		}
		mode = "check"
	}
	var findings []checks.Finding
	switch mode {
	case "local":
		findings = checks.RunLocal(context.Background(), dir)
	case "fleet":
		findings, err = checks.RunFleet(context.Background())
		if err != nil {
			return err
		}
	default:
		findings = checks.Run(dir)
	}
	if len(findings) == 0 {
		if fixErr != nil {
			return fixErr
		}
		fmt.Println("repo-standard: ok")
		return nil
	}
	if fixErr != nil {
		fmt.Fprintln(os.Stderr, "repo-standard: fix:", fixErr)
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	fmt.Printf("repo-standard: %d finding(s)\n", len(findings))
	return errFindings
}

// runSandbox implements one of the two writes repo-standard performs: rewriting a repo's
// .sandbox/lib from the canonical copy. Checking reports drift; this repairs
// it, so the repair command a finding prints is a command that exists.
func runSandbox(args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		usage()
		return fmt.Errorf("%w: repo-standard sandbox takes exactly one subcommand, sync", errUsage)
	}
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find the working directory: %w", err)
	}
	changed, err := sandbox.Sync(dir)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Println("repo-standard: sandbox library already canonical")
		return nil
	}
	for _, name := range changed {
		fmt.Println("repo-standard: synced", sandbox.LibDir+"/"+name)
	}
	fmt.Printf("repo-standard: %d file(s) synced\n", len(changed))
	return nil
}

// runPack rewrites a repo's .golangci-rules/ from the embedded lintbrush pack:
// the repair the lint-pack rule names.
func runPack(args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		usage()
		return fmt.Errorf("%w: repo-standard pack takes exactly one subcommand, sync", errUsage)
	}
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find the working directory: %w", err)
	}
	changed, err := lintpack.Sync(dir)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Println("repo-standard: lint rules pack already canonical")
		return nil
	}
	for _, name := range changed {
		fmt.Println("repo-standard: synced", lintpack.Dir+"/"+name)
	}
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: repo-standard [--local | --fleet | --fix]
       repo-standard init <repo> [flags]
       repo-standard sandbox sync
       repo-standard pack sync
       repo-standard version

  (no flag)  check in-repo files against the fleet contract
  --local    check machine-local wiring (hooksPath, hooks, dolt remote)
  --fleet    check GitHub-side settings across the fleet (gh auth)
  --fix      make the in-repo repairs that need no judgment, then check.
             Only ever creates what is absent; never rewrites your prose.
  init       scaffold a new repo that passes the in-repo checks unedited,
             then wire the machine half. GitHub state untouched unless
             --with-remote is passed; repo-standard init -h for flags.
  sandbox sync
             rewrite .sandbox/lib from the canonical copy repo-standard ships
  pack sync  rewrite .golangci-rules/ from the lintbrush rules pack
             repo-standard ships; an opt-in rule file is written only
             where the repo already holds it
  version    print the rev the binary was built from
`)
}
