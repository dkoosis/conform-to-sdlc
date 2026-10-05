# conform-to-sdlc

★ One program, pinned in every dkoosis Go repo, fails `make check` when that repo's setup — Makefile targets, lint rules, CI workflow, git hooks, bd config, GitHub settings — drifts from the fleet standard, and names the command that puts it back.

Fleet SDLC conformance checker for the dkoosis repos. One contract — same
Makefile verbs, lint core, CI shape, hooks, bd config, GitHub settings — checked
instead of copied, so improvements propagate as pin bumps and templates can't
drift.

Three surfaces, because the three kinds of state live in three places:

| surface | sees | runs |
|---|---|---|
| `conform-to-sdlc` | in-repo files: Makefile verbs, `.golangci.yml` core, single pin, CI-calls-make-check behind a docs-only skip, bd config | every `make check`, hard-fail |
| `conform-to-sdlc --local` | machine wiring CI can't see: `core.hooksPath`, hooks executable, dolt remote | `make doctor`, session start |
| `conform-to-sdlc --fleet` | GitHub: branch protection, labels, merge policy | promulgation + pin-bump sweeps |
| `conform-to-sdlc --fix` | the same in-repo files, but writes the ones that are simply absent, then checks | adopting a new rule; scaffolding |

Principles:

- **Never soft-fail.** A rule too noisy to hard-fail gets deleted, not warned.
- **Semantic comparison, not bytes.** The lint core is a parsed set; byte-diffing
  YAML makes rules noisy.
- **Failures name file, rule, repair command.**
- **<1s** for the in-`check` surface, or it gets bypassed.
- **Dogfooded.** This repo's CI runs conform-to-sdlc on itself.
- **`--fix` only ever creates what is absent.** It never rewrites a file a
  human wrote, and the skeleton it writes is deliberately still red — a
  scaffold that passed the gate would read as work done and carry none.

Non-goals:

- Being a template tool. Templates drift the moment they are copied; that is
  the problem this exists to replace.
- Soft-fail or warning levels. A rule earns hard-fail or it is deleted.
- Per-repo variants of the contract. A repo declares an exception by rule id
  in `docs/conform.json`, with a reason — it does not fork the rule.
- Formatting or style opinions already owned by `.golangci.yml`.
- Requiring what sdlc has no place for. sdlc's `SPEC.md` says where an epic's
  state and the North Star live; a rule here that asks a repo for another copy
  is deleted.

The sdlc repo does not pin this checker. It is a Claude Code plugin with a
smaller floor of its own, stated in its `AGENTS.md`; the rules here about how a
repo steers agents match how sdlc does it, and the Makefile, lint and CI rules
are the fleet's, for the Go tools.

Reference repo: [ferret](https://github.com/dkoosis/ferret). Distributed as a
pinned Go module; adopting a new rule version is a deliberate PR per repo.
