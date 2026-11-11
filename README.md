# repo-standard

★ One program, pinned in every dkoosis Go repo, fails `make check` when that repo's setup — Makefile targets, lint rules, CI workflow, git hooks, bd config, GitHub settings — drifts from the fleet standard, and names the command that puts it back.

Fleet SDLC conformance checker for the dkoosis repos. One contract — same
Makefile verbs, lint core, CI shape, hooks, bd config, GitHub settings — checked
instead of copied, so improvements propagate as pin bumps and templates can't
drift.

Renamed from conform-to-sdlc on 2026-11-11: the exceptions file is now `docs/repo-standard.json` and the make target `standard` (was `selfcheck`).

Three surfaces, because the three kinds of state live in three places:

| surface | sees | runs |
|---|---|---|
| `repo-standard` | in-repo files: Makefile verbs, `.golangci.yml` core and the canonical linter set, `.golangci-rules/` byte-equal to the lintbrush pack it embeds, single pin, CI-calls-make-check behind a docs-only skip, bd config | every `make check`, hard-fail |
| `repo-standard --local` | machine wiring CI can't see: `core.hooksPath`, hooks executable, dolt remote | `make doctor`, session start |
| `repo-standard --fleet` | GitHub: branch protection, labels, merge policy | promulgation + pin-bump sweeps |
| `repo-standard --fix` | the same in-repo files, but writes the ones that are simply absent (README, CI workflow, `.golangci.yml`, `.golangci-rules/`), then checks | adopting a new rule; scaffolding |
| `repo-standard pack sync` | `.golangci-rules/`: rewrites the lintbrush pack files from the embedded copy | the repair lint-pack names |

Principles:

- **Never soft-fail.** A rule too noisy to hard-fail gets deleted, not warned.
- **Semantic comparison, not bytes.** The lint core is a parsed set; byte-diffing
  YAML makes rules noisy. The lint rules pack is the exception: it is Go source
  shipped whole, so lint-pack compares it byte for byte. Its canon is
  dkoosis/lintbrush `gorules/`; `make pack-refresh` copies a lintbrush commit
  into the embed, and a repo-standard release carries it to the fleet.
- **Failures name file, rule, repair command.**
- **<1s** for the in-`check` surface, or it gets bypassed.
- **Dogfooded.** This repo's CI runs repo-standard on itself.
- **`--fix` only ever creates what is absent.** It never rewrites a file a
  human wrote, and the skeleton it writes is deliberately still red — a
  scaffold that passed the gate would read as work done and carry none.

Non-goals:

- Being a template tool. Templates drift the moment they are copied; that is
  the problem this exists to replace.
- Soft-fail or warning levels. A rule earns hard-fail or it is deleted.
- Per-repo variants of the contract. A repo declares an exception by rule id
  in `docs/repo-standard.json`, with a reason — it does not fork the rule.
- Formatting or style opinions already owned by `.golangci.yml`.
- Requiring what sdlc has no place for. sdlc's `SPEC.md` says where an epic's
  state and the North Star live; a rule here that asks a repo for another copy
  is deleted.

## Rules the audit and lint standard adds

Every finding names the file, the rule id and the repair. These ids are the
ones a repo meets when it adopts the standard; `docs/repo-standard.json` excepts
a rule by id with a reason.

`make audit` runs `check`, then seven gates, each a pinned tool that fails the
target: race, fuzz, dupe, nilcheck, deadcode, omitempty and vuln. A gate that
skips when its tool is absent, or runs a tool with no pinned version, fails
the repo. `omitempty` runs `go run github.com/dkoosis/repo-standard/cmd/omitemptyscalar@$(OMITEMPTYSCALAR_VER) ./...`
(the analyzer ships in this module, first at v0.4.0; the version sits beside the
other pins), which exits 3 on a `,omitempty` tag on a bool, where a
`false` is lost on write; a deliberate case takes
`//omitemptyscalar:ignore <reason>` on the field (the reason is required; golangci-lint
warns "unknown linters" about the older `//nolint:omitemptyscalar` and no setting
turns the warning off, so the marker is one golangci-lint does not read).
repo-standard itself cannot pin its own unreleased tag, so its Makefile runs
`go run ./cmd/omitemptyscalar ./...`; `tool-pin` accepts a local-path run only in a
repo whose `go.mod` module line is `github.com/dkoosis/repo-standard`.

| rule | fails a Go repo when | repair |
|---|---|---|
| `makefile-verbs` | `audit` does not reach race, fuzz, dupe, nilcheck, deadcode, omitempty and vuln | add the target and list it in `audit`'s prerequisites; `--fix` scaffolds a new Makefile with all of them |
| `gate-skip` | a check or audit step exits 0 when its tool is missing | delete the presence test, or make the missing branch `exit 1` |
| `tool-pin` | a gate tool runs from PATH, at `@latest`, or at an unset version | `go run <pkg>@$(TOOL_VER)` or `npx tool@$(TOOL_VER)`, with the version set |
| `fuzz-coverage` | a `Fuzz*` function is not run by the fuzz target | make the target run every Fuzz function |
| `ci-audit-schedule` | no workflow runs `make audit` on a schedule | add a scheduled workflow; `--fix` writes one |
| `lint-pack` | `.golangci-rules/` differs by one byte from the embedded pack, or a repo requiring `github.com/dkoosis/atomicfile` lacks `optin_rawstatewrite.go` or does not list it in gocritic's ruleguard `rules` | `repo-standard pack sync`; then append the file to `rules` |
| `lint-set` | a canonical linter is off, or a setting is weakened, including errcheck without `check-blank: true` | the finding's repair; `check-blank` is on for non-test code and exempt in `_test.go` files by the `exclusions.rules` entry the lintbrush baseline ships |

The raw-write rule (pack rule 2, `optin_rawstatewrite.go`) turns on by
dependency: a `go.mod` that requires `github.com/dkoosis/atomicfile` directly
(an `// indirect` line does not count) must carry the file and name it. The
atomicfile repo is exempt: its `go.mod` declares that module path, so it is the
implementation, and it holds the raw writes the rule flags.

## Canonical linter set

`lint-set` fails a repo whose `.golangci.yml` does not enable each linter below,
or weakens one of these settings: errcheck's `check-blank: true` (a blank-discarded
`x, _ := f()` fails; test files are exempt), nolintlint's `require-explanation` and
`require-specific` (and no `allow-unused`), errcheck's `exclude-functions`
beyond the canonical list, gocritic's ruleguard wiring of the lintbrush pack
(`bugclasses.go` and `modern.go`, `failOn: dsl`), and forbidigo's
`context.Background` pattern with `analyze-types`. The set and its settings are
the embedded `internal/checks/golangci.canonical.yml`, which `--fix` writes
into a repo that has no `.golangci.yml`. A linter that is enabled but excluded
on every file by `linters.exclusions` counts as off. A repo may enable more. It
drops a linter only by name and reason in `docs/repo-standard.json`:
`{"rule": "lint-set", "drop": {"<linter>": "<reason>"}}`. It keeps a weaker
setting the same way, keyed by the setting's path under `linters.settings`, as
`"nolintlint.allow-unused"`; the finding names the key.

The set is the union of what ferret, sdlc, loto, canapay and trixi enable
(golangci-lint v2.14.0), 66 linters. ferret's 62 are the base; the others name
the repo that brought them.

| linter | source | catches |
|---|---|---|
| asciicheck | ferret | non-ASCII identifiers that look like ASCII ones |
| bodyclose | ferret | an HTTP response body never closed, which leaks the connection |
| containedctx | ferret | a context stored in a struct, where it outlives its request |
| copyloopvar | ferret | loop-variable copies made redundant by Go 1.22 |
| cyclop | ferret | functions too branchy to test every path |
| dupl | sdlc | copy-pasted blocks, before the copies diverge and one keeps a bug |
| dupword | ferret | doubled words in comments and strings |
| durationcheck | ferret | two durations multiplied, a unit bug |
| err113 | ferret | errors built inline, which callers cannot match with errors.Is |
| errcheck | ferret | an error return dropped |
| errchkjson | ferret | an unchecked JSON encode error, or one checked that cannot fail |
| errname | ferret | error types and sentinels named off convention |
| errorlint | ferret | errors compared with == or type-asserted where wrapping breaks it |
| exhaustive | ferret | a switch over an enum that misses a case |
| exptostd | ferret | golang.org/x/exp calls the standard library now provides |
| fatcontext | ferret | a context rebuilt in a loop, which grows without bound |
| forbidigo | ferret | lintbrush pack rule 5: context.Background deep in a daemon |
| forcetypeassert | ferret | a type assertion that panics on the wrong type |
| funlen | ferret | functions too long to read whole |
| gocheckcompilerdirectives | ferret | a //go: directive with a typo, which the compiler ignores |
| gochecknoinits | ferret | init functions, hidden start-up order |
| gocognit | ferret | functions too nested to follow |
| goconst | ferret | a repeated literal that should be one constant |
| gocritic | ferret | diagnostic and performance checks, and runs the lintbrush ruleguard pack |
| gocyclo | ferret | functions with too many paths |
| godoclint | canapay | a doc comment that names a symbol other than its own |
| gosec | ferret | security mistakes: weak modes, injection, unchecked input |
| govet | ferret | the compiler-adjacent vet checks |
| grouper | ferret | declaration groups split off convention |
| inamedparam | ferret | interface methods with unnamed parameters |
| ineffassign | ferret | an assignment never read |
| intrange | ferret | counting loops that range over an int |
| iotamixing | ferret | iota consts mixed with plain ones, which shifts values |
| loggercheck | ferret | odd key-value pairs passed to a logger |
| makezero | ferret | append to a slice made with a non-zero length |
| mirror | ferret | string and []byte conversions where a mirror function exists |
| misspell | ferret | misspelled English in comments and strings |
| modernize | ferret | code the current Go version writes more simply |
| musttag | ferret | a struct marshalled without field tags |
| nakedret | ferret | naked returns in long functions |
| nestif | ferret | deeply nested ifs |
| nilerr | ferret | return nil right after an error check, which swallows the error |
| nilnesserr | ferret | returning a different, nil error after an error check |
| nilnil | ferret | return nil, nil, which leaves callers to guess |
| noctx | ferret | HTTP and exec calls without a context, which cannot be cancelled |
| nolintlint | ferret | a //nolint without a reason or a named linter |
| perfsprint | ferret | fmt.Sprintf where strconv or concatenation does |
| prealloc | ferret | slices that could be allocated once |
| predeclared | ferret | a name that shadows a built-in |
| reassign | ferret | a package variable from another package reassigned |
| recvcheck | ferret | value and pointer receivers mixed on one type |
| revive | ferret | context-as-argument, error-return, dot-imports and the other enabled rules |
| rowserrcheck | ferret | sql Rows.Err not checked after the loop |
| sloglint | ferret | slog calls with mixed or odd key-value shapes |
| sqlclosecheck | ferret | sql Rows or Stmt never closed |
| staticcheck | ferret | the SA correctness suite and the rest of staticcheck |
| testifylint | ferret | testify assertions misused |
| thelper | ferret | test helpers without t.Helper |
| unconvert | ferret | conversions to the type a value already has |
| unparam | ferret | parameters every caller passes the same value |
| unqueryvet | loto | SELECT * in SQL, which breaks when a column is added |
| unused | ferret | unused code |
| usestdlibvars | ferret | literals where a standard-library constant exists |
| usetesting | ferret | os calls in tests where testing has a scoped version |
| wastedassign | ferret | an assignment overwritten before it is read |
| wrapcheck | canapay, trixi | an error from another package returned bare, with no context of where it failed |

Dropped:

| linter | source | why dropped |
|---|---|---|
| depguard | trixi | it checks import rules each repo must write for its own layout; with no rules it flags every import outside the standard library. A repo with architecture rules enables it on top of the set. |

The sdlc repo does not pin this checker. It is a Claude Code plugin with a
smaller floor of its own, stated in its `AGENTS.md`; the rules here about how a
repo steers agents match how sdlc does it, and the Makefile, lint and CI rules
are the fleet's, for the Go tools.

Reference repo: [ferret](https://github.com/dkoosis/ferret). Distributed as a
pinned Go module; adopting a new rule version is a deliberate PR per repo.
