# repo-standard Makefile — strict QA gates, fleet pattern (reference: ~/Projects/ferret).
#
# Primary: check (vet+lint+test+build) | audit (check+race+fuzz+dupe+nilcheck+deadcode+omitempty+vuln)

.DEFAULT_GOAL := check

SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c

# ── Shared sandbox (go-sandbox) ──
# doctor ← Makefile.doctor.mk; cross / cross-amd64 / cross-arm64 ← Makefile.cross.mk
include .sandbox/lib/Makefile.doctor.mk
include .sandbox/lib/Makefile.cross.mk

.PHONY: help check audit vet lint test race fuzz build vuln dupe nilcheck deadcode omitempty install deploy clean pack-refresh

# Serialize golangci-lint through the machine-global mkdir mutex (see script
# header — golangci-lint's cache lock fails exit-3 on contention instead of
# waiting, which cascades across parallel sessions/worktrees).
GOLANGCILINT := bash scripts/lint-locked

# 12-char rev stamped into the binary at install time; `repo-standard version`
# prints it. A plain `go build` with no ldflags leaves main.version at its
# "unknown" default.
VERSION := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION)

help: ## Show this help
	@printf '\n\033[1mFour verbs. Identical in every dkoosis repo.\033[0m\n\n'
	@printf '  \033[36mcheck \033[0m  fast gate — vet + lint + test + build. Pre-commit; required in CI.\n'
	@printf '  \033[36maudit \033[0m  check, plus race + vuln + dupe + nilcheck. Before you ask for review.\n'
	@printf '  \033[36mdeploy\033[0m  build + install this tool locally.\n'
	@printf '  \033[36mhelp  \033[0m  this text.\n\n'
	@printf 'Everything below is an internal step of one of those four. Call the verbs.\n\n'
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_.-]+:.*?## / { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@printf '\n'

check: vet lint test build standard ## Fast validation: vet + lint + test + build + repo-standard on itself
	@echo "=== check pass ==="

audit: check race fuzz dupe nilcheck deadcode omitempty vuln ## Exhaustive validation
	@echo "=== audit pass ==="

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (strict config)
	$(GOLANGCILINT) run ./...

# No -count=1: Go's test cache stays ON for the dev loop; race/audit bypass it.
test: ## Run tests with coverage
	go test -cover ./...

race: ## Run tests with race detector (fresh run)
	go test -race -count=1 -cover ./...

build: ## Compile everything
	go build ./...

# Dogfood (cfm-1e1.7): repo-standard is fleet repo #14 and gates itself, or it is
# the unwatched watcher — the exact L4 failure it exists to kill.
standard: ## Run repo-standard against this repo (dogfood gate)
	go run ./cmd/repo-standard

# doctor's core steps come from .sandbox/lib/Makefile.doctor.mk (shared,
# byte-identical fleet-wide); wiring repo-standard --local in as an extra prereq
# keeps the lib untouched (cfm-1e1.3).
doctor: standard-local ## Machine doctor (sandbox lib) + repo-standard --local
standard-local: ## Run repo-standard --local (machine wiring: hooksPath, bd hooks, live bd config)
	go run ./cmd/repo-standard --local

vuln: ## Scan for known vulnerabilities (govulncheck, pinned)
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER) ./...

# jscpd's --gitignore flag no longer exists (silent no-op in older Makefiles);
# --ignore with explicit globs is the working form.
JSCPD_VER ?= 5.1.2
dupe: ## Check for code duplication (jscpd, pinned)
	TMP_JSCPD=$$(mktemp -d); npx --yes jscpd@$(JSCPD_VER) . --ignore "**/.git/**,**/.sandbox/**,**/.beads/**" --output $$TMP_JSCPD; rm -rf $$TMP_JSCPD

nilcheck: ## Run nilaway (pinned)
	go run go.uber.org/nilaway/cmd/nilaway@$(NILAWAY_VER) -include-pkgs=github.com/dkoosis/repo-standard ./...

# Runs each Fuzz function it finds, FUZZTIME apiece.
FUZZTIME ?= 10s
fuzz: ## Fuzz every Fuzz test (FUZZTIME=10s each)
	@set -e; \
	for pkg in $$(go list ./...); do \
		for f in $$(go test -list '^Fuzz' $$pkg | sed -n '/^Fuzz/p'); do \
			echo "fuzz $$pkg $$f"; \
			go test -run '^$$' -fuzz "^$$f$$" -fuzztime=$(FUZZTIME) $$pkg; \
		done; \
	done

# deadcode lists each function no path from main reaches, and exits 0 either way,
# so the target fails on any output.
DEADCODE_VER ?= v0.52.0
deadcode: ## Fail on any function unreachable from main (deadcode)
	@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/repo-standard) || { echo "deadcode: could not run"; exit 1; }; \
	if [ -n "$$out" ]; then echo "$$out"; echo "deadcode: the functions above are unreachable; cut them"; exit 1; fi

# omitemptyscalar exits 3 on a ,omitempty tag on a bool (a false that is lost on
# write). Other repos run it as go run github.com/dkoosis/repo-standard/cmd/
# omitemptyscalar@$(OMITEMPTYSCALAR_VER); this repo cannot pin its own
# unreleased tag, so it runs the tree it is checking (tool-pin allows that here
# only, by the go.mod module line).
omitempty: ## Fail on a bool field tagged omitempty (omitemptyscalar, from this tree)
	go run ./cmd/omitemptyscalar ./...

## doctor target provided by .sandbox/lib/Makefile.doctor.mk (project.conf-driven)
## cross / cross-amd64 / cross-arm64 provided by .sandbox/lib/Makefile.cross.mk

install: ## Install repo-standard to GOBIN, else GOPATH/bin (version-stamped)
	go install -ldflags "$(LDFLAGS)" ./cmd/repo-standard

# go install writes to GOBIN when set, else the first GOPATH entry's bin.
deploy: build install ## Build, then install repo-standard to GOBIN or GOPATH/bin
	@bindir="$$(go env GOBIN)"; \
	[ -n "$$bindir" ] || bindir="$$(go env GOPATH | cut -d: -f1)/bin"; \
	fresh="$$bindir/repo-standard"; \
	echo "=== deployed (repo-standard installed to $$bindir) ==="; \
	active="$$(command -v repo-standard 2>/dev/null || true)"; \
	if [ -n "$$active" ] && ! [ "$$active" -ef "$$fresh" ]; then \
		echo "warning: PATH runs $$active, not the fresh $$fresh; the stale copy shadows this deploy"; \
	fi

clean: ## Remove built binary + sandbox build artifacts
	@rm -f repo-standard
	@rm -rf .sandbox/bin/linux-amd64 .sandbox/bin/linux-arm64 .sandbox/cache
	@echo "=== clean ==="

# The lint rules pack is embedded (internal/lintpack/_gorules) and standard's
# lint-pack rule compares .golangci-rules/ with it, offline. To take a new pack,
# commit it to dkoosis/lintbrush, then run this against that checkout.
LINTBRUSH ?= $(HOME)/Projects/dk/lintbrush
pack-refresh: ## Refresh the embedded lintbrush rules pack from LINTBRUSH's HEAD and sync .golangci-rules/
	bash scripts/pack-refresh "$(LINTBRUSH)"
