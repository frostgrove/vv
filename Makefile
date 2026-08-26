.PHONY: help test unit integration examples up down logs psql mysql mariadb fmt vet tidy generate corpus ent version release clean \
        work check check-deps check-tiers check-todo check-tidy check-utils check-triplets vuln api

GO  ?= go
MOD := github.com/frostgrove/vv

# Every module in the repository, discovered rather than listed. A hand-written
# list is how a new module escapes unit, vet, tidy and release all at once, and
# this repository has been bitten by exactly that: `fmt` kept a second list and
# `go.work` a third, and neither knew about anything added later.
ALL_MODULES := $(shell find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort)

# The published ones. `test` and `_examples` exist to keep drivers, ORMs and
# example stacks out of the library's dependency graph, and are never tagged.
UNPUBLISHED := ./test ./_examples
MODULES     := $(filter-out $(UNPUBLISHED),$(ALL_MODULES))

# Published modules other than the root, i.e. the satellites that carry one
# dependency decision each and resolve the library the way a consumer does.
SATELLITES := $(filter-out .,$(MODULES))

# What go.work joins. `_examples` is deliberately outside it — it demonstrates
# every stack at once, so it builds only under `make examples` with GOWORK=off.
WORKSPACE := $(filter-out ./_examples,$(ALL_MODULES))

# The contract manifest (D-035). These import the standard library and each
# other, and nothing else; `check-tiers` is what makes that true rather than
# aspirational.
#
# Every entry is one package path, matched exactly, and the arm lists `./$p`
# rather than `./$p/...`. Both halves of that became load-bearing when the tree
# grew subtrees under a manifest name ([[D-058]]): a prefix match would let
# `crud/sqlrepo` in under `crud`, and a recursive listing would drag every
# package below `crud/` into the very set being checked. Either one alone turns
# this target into a green light that means nothing. A new contract package is
# added here by hand, which is what D-048 asks for anyway.
TIER0 := crud crud/crudtest crud/query errs errs/sqlerr port port/porthttp

# `errs` is sealed tighter than the rest of the manifest. D-036's case rests on
# it having an empty require block at the first tag, and the arm above cannot
# see an import of `crud`: the filter drops every contract package, so the
# violation would stay invisible until the tag turned it into a require cycle
# against a root that requires `errs`. Scoped to the prefix rather than the
# package, because phase 2's parsers in `errs/sqlerr` legitimately import `errs`
# and both end up inside the same module.
#
# The sealed arm alone passes `-test`. `go mod tidy` counts test imports, so a
# `crud` import in `errs`' test package becomes a require in the module `errs`
# is split into, and today nothing else sees it: the import is intra-module, so
# `check-tidy` stays green and `check-deps` finds nothing third-party. `-test`
# adds the synthetic `errs.test` binary and the `errs_test` external package to
# the list, which the second `sed` normalises away. The plain arm above stays
# without it on purpose — `crud`'s own tests import `crud/sqlrepo`, which is legal
# inside one module and only illegal across a module boundary.
TIER0_SEALED := errs

# `crud` is sealed in the other direction, and by D-016's surviving half rather
# than D-036: no file in package `crud` outside `_test.go` imports anything but
# the standard library, and that is what makes `crud` unable to import `errs` —
# the one rule `errs/doc.go` states and cannot express in a signature. The plain
# arm cannot see that import either: its filter drops every contract package, so
# the two classification paths `errs/doc.go` warns about would first disagree at
# run time. Without `-test`: `crud`'s own tests import `crud/sqlrepo`, which is
# legal inside one module.
#
# The scope is the package, not the subtree. D-016 was written when `crud/` held
# one package and the two readings were the same sentence; [[D-058]] moved
# `sqlrepo`, `query`, `catalog` and the rest underneath it, and every one of them
# is allowed the dependencies this arm forbids. Listed as `./crud`, never
# `./crud/...`.
TIER0_STDLIB := crud

# The subsystems, for the one rule that points the other way. [[D-058]] gives
# `utils/` its only boundary — nothing under it may import one of these — and
# without an arm that boundary is a sentence in a document. It is what puts
# `vvdb` under `utils/` despite `vvdb` carrying a module of its own, so it is
# load-bearing rather than tidy: the day a "utility" reaches for the repository
# it has stopped being one, and this is what says so.
SUBSYSTEMS := crud auth port remote

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

unit: ## Run the unit tests of every module (no database needed), under -race
	@for m in $(WORKSPACE); do echo "==> $$m"; (cd $$m && $(GO) test -race ./...) || exit 1; done
	@echo "==> ./_examples"; cd _examples && GOWORK=off $(GO) test -race ./...

# Under the race detector, and that is not caution. This suite is the only thing
# in the repository that touches live drivers, real connection pools and the
# concurrency they bring, and the library holds process-global state that every
# repository over a model shares — the schema cache and the per-handle catalog.
# A race there is not a race between two repositories but between any two
# concurrent requests, which is the shape that never reproduces in a unit test.
# It costs three seconds.
integration: up ## Run the integration suite against Postgres and MySQL, under -race
	$(GO) test -race -tags=integration -count=1 ./test/...

test: unit integration ## Everything

examples: ## Build and vet the runnable examples (needs the databases to run them)
	cd _examples && GOWORK=off $(GO) build ./... && GOWORK=off $(GO) vet ./... && GOWORK=off $(GO) test ./...

up: ## Start the databases and wait for them
	docker compose up -d --wait

down: ## Stop and remove the databases
	docker compose down -v

logs: ## Tail the database logs
	docker compose logs -f

psql: ## Open a psql shell
	docker compose exec postgres psql -U vv -d vv

mysql: ## Open a mysql shell
	docker compose exec mysql mysql -uvv -pvv vv

mariadb: ## Open a mariadb shell
	docker compose exec mariadb mariadb -uvv -pvv vv

fmt: ## gofmt everything
	gofmt -l -w .

vet: ## go vet every module, including the test module
	@for m in $(WORKSPACE); do echo "==> $$m"; (cd $$m && $(GO) vet ./...) || exit 1; done
	@echo "==> ./_examples"; cd _examples && GOWORK=off $(GO) vet ./...
	$(GO) vet -tags=integration ./test/...

# ---------------------------------------------------------------------------
# checks
#
# These exist because `go.work` hides the thing they look for. The workspace
# build list is the union of every member, so a root-module package importing a
# satellite's dependency builds green, vets green, and leaves go.mod untouched —
# while a consumer gets `no required module provides package`. `make unit` and
# `make vet` cannot see it. These can.

check: check-deps check-tiers check-utils check-triplets check-todo check-tidy ## Every structural check

# Not part of `check`, because it reaches the network and `check` must run
# offline. It belongs to the release, and `release` runs it.
#
# In workspace mode on purpose. With GOWORK=off a satellite cannot resolve the
# library — there is no tag yet — and govulncheck reports a loading error rather
# than a clean scan, which is the shape that reads as "nothing found". Ten of the
# eleven published modules scanned that way the first time and none of them
# actually ran.
# The baseline a release is diffed against.
#
# Nothing checks it automatically and nothing should: a diff here is a question
# for a person, not a pass/fail. After v0.1.0 a line that disappears is a
# breaking change and a line that changes shape is one too, and the only way to
# see either is to regenerate and read.
api: ## Regenerate docs/api/surface.md, the exported-surface baseline
	@mkdir -p docs/api
	@{ echo "# Exported surface at the first tag"; echo; \
	   echo "Generated by \`make api\`. It is a baseline, not a description: after v0.1.0 a"; \
	   echo "line that disappears from this file is a breaking change, and a line that"; \
	   echo "changes shape is one too. Regenerate and read the diff before every release."; echo; \
	   for m in . $(SATELLITES); do \
	     for pkg in $$(cd $$m && $(GO) list ./... 2>/dev/null | grep -v "/internal/"); do \
	       out=$$($(GO) doc -short "$$pkg" 2>/dev/null | grep -v "^$$"); \
	       if [ -n "$$out" ]; then echo "## $$pkg"; echo '```go'; echo "$$out"; echo '```'; echo; fi; \
	     done; \
	   done; } > docs/api/surface.md
	@echo "api: docs/api/surface.md regenerated — read the diff"

vuln: ## Scan every module for known vulnerabilities
	@fail=0; \
	for m in . $(SATELLITES) ./test ./_examples; do \
		printf "%-26s " "$$m"; \
		if [ "$$m" = "./_examples" ]; then env="GOWORK=off"; else env=""; fi; \
		out=$$(cd $$m && env $$env $(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./... 2>&1); \
		if echo "$$out" | grep -q "No vulnerabilities found"; then \
			echo "ok"; \
		else \
			echo "AFFECTED"; \
			echo "$$out" | grep -E "Vulnerability #|Module:|Found in:|Fixed in:|errors with" | sed "s/^/    /"; \
			fail=1; \
		fi; \
	done; \
	if [ $$fail -ne 0 ]; then exit 1; fi; \
	echo "vuln: ok"

# The check D-033 used to carry ended in `grep '\.'`, which matches
# standard-library paths like crypto/internal/entropy/v1.0.0 — it printed 17
# lines on a clean tree and so had no pass/fail meaning. `.Standard` is the
# question that was meant.
check-deps: ## The root module has no third-party dependency (D-016, D-033)
	@out=$$($(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>/dev/null \
	        | grep -v '^$(MOD)' || true); \
	if [ -n "$$out" ]; then \
		echo "the root module has third-party dependencies:"; echo "$$out" | sed 's/^/  /'; exit 1; \
	fi
	@for m in $(SATELLITES); do \
		out=$$(cd $$m && $(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>/dev/null \
		       | grep -v '^$(MOD)' || true); \
		echo "$$m: $$(echo "$$out" | grep -c . ) external packages"; \
	done
	@echo "check-deps: ok"

# Every arm lists first and filters second, rather than piping a silenced
# `go list` straight into grep. Silenced, a package that does not compile
# produces no output, an empty result reads as "imports nothing outside the
# manifest", and the check reports ok on a tree that does not build — which is
# the one answer it must never give.
check-tiers: ## A contract package imports only stdlib and other contract packages
	@fail=0; re=$$(echo "$(TIER0)" | tr ' ' '|'); \
	for p in $(TIER0); do \
		[ -d "$$p" ] || continue; \
		deps=$$($(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./$$p 2>&1) \
		  || { echo "cannot list $$p — it does not build:"; echo "$$deps" | sed 's/^/  /'; fail=1; continue; }; \
		bad=$$(echo "$$deps" | grep "^$(MOD)/" | sed 's|^$(MOD)/||' \
		      | grep -Ev "^($$re)$$" || true); \
		if [ -n "$$bad" ]; then \
			echo "contract package $$p imports non-contract packages:"; \
			echo "$$bad" | sort -u | sed 's/^/  /'; fail=1; \
		fi; \
	done; \
	for p in $(TIER0_STDLIB); do \
		[ -d "$$p" ] || continue; \
		deps=$$($(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./$$p 2>&1) \
		  || { echo "cannot list $$p — it does not build:"; echo "$$deps" | sed 's/^/  /'; fail=1; continue; }; \
		bad=$$(echo "$$deps" | grep "^$(MOD)" | sed 's|^$(MOD)/||' \
		      | grep -Ev "^$$p$$" || true); \
		if [ -n "$$bad" ]; then \
			echo "stdlib-only package $$p may import only the standard library:"; \
			echo "$$bad" | sort -u | sed 's/^/  /'; fail=1; \
		fi; \
	done; \
	for p in $(TIER0_SEALED); do \
		[ -d "$$p" ] || continue; \
		deps=$$($(GO) list -deps -test -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./$$p/... 2>&1) \
		  || { echo "cannot list $$p — it does not build:"; echo "$$deps" | sed 's/^/  /'; fail=1; continue; }; \
		bad=$$(echo "$$deps" | grep "^$(MOD)" | sed 's|^$(MOD)/||' \
		      | sed -E 's/ \[.*\]$$//; s/\.test$$//; s/_test$$//' \
		      | grep -Ev "^$$p(/|$$)" || true); \
		if [ -n "$$bad" ]; then \
			echo "sealed package $$p may import only the standard library and $$p/...:"; \
			echo "$$bad" | sort -u | sed 's/^/  /'; fail=1; \
		fi; \
	done; \
	if [ $$fail -ne 0 ]; then exit 1; fi; \
	echo "check-tiers: ok"

# Both halves of `utils/` are listed, and they need different commands: `vvflag`
# and `vvdb` are packages of the root module, `vvcfg` and `vvdb/dbpgx` are
# modules of their own and are invisible to a root-module `go list`. Listing only
# the first half would leave the two packages most likely to reach for a
# subsystem unchecked.
check-utils: ## Nothing under utils/ imports a subsystem (D-058)
	@re=$$(echo "$(SUBSYSTEMS)" | tr ' ' '|'); \
	deps=$$( { $(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./utils/... || exit 1; \
	           for m in $(filter ./utils/%,$(MODULES)); do \
	             (cd $$m && $(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...) || exit 1; \
	           done; } 2>&1 ) \
	  || { echo "cannot list utils/ — it does not build:"; echo "$$deps" | sed 's/^/  /'; exit 1; }; \
	bad=$$(echo "$$deps" | grep "^$(MOD)/" | sed 's|^$(MOD)/||' \
	      | grep -E "^($$re)(/|$$)" || true); \
	if [ -n "$$bad" ]; then \
		echo "a package under utils/ imports a subsystem — it is not a utility (D-058):"; \
		echo "$$bad" | sort -u | sed 's/^/  /'; exit 1; \
	fi; \
	echo "check-utils: ok"

# A triplet is three bindings over the same contract, and the rule is that they
# carry the same test names file for file. Where a framework genuinely differs,
# the difference goes in routing_test.go and is written down in FL-013.
#
# The rule was in CLAUDE.md and nowhere else, so it held by everybody
# remembering it — and it had already stopped holding: crudfiber was the one
# binding of three with no routing_test.go, which is the file the rule points at,
# on the one framework whose router matches in registration order. This is what
# notices next time.
TRIPLETS := crud/http/crudnet,crud/http/crudgin,crud/http/crudfiber \
            auth/http/authnet,auth/http/authgin,auth/http/authfiber

# names_of prints one binding's test names, minus TestMain and minus whatever
# routing_test.go or binding_test.go declares — those two files are where a
# difference between frameworks is allowed to live, and naming it there is what
# makes it deliberate. routing_test.go is the CRUD triplet's name for it, because
# what differs there is how a router resolves a path; binding_test.go is the auth
# triplet's, because what differs there is what the framework does with an error
# nobody asked it about.
define names_of
grep -ho '^func Test[A-Za-z0-9_]*' $(1)/*_test.go 2>/dev/null | sed 's/^func //' \
  | grep -v '^TestMain$$' | sort -u > $(2).all; \
grep -ho '^func Test[A-Za-z0-9_]*' $(1)/routing_test.go $(1)/binding_test.go 2>/dev/null \
  | sed 's/^func //' | sort -u > $(2).exempt; \
comm -23 $(2).all $(2).exempt > $(2)
endef

check-triplets: ## Each transport triplet carries the same test names (CLAUDE.md)
	@fail=0; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	for set in $(TRIPLETS); do \
		ref=""; refd=""; \
		for d in $$(echo "$$set" | tr ',' ' '); do \
			if [ ! -d "$$d" ]; then \
				echo "$$d is named as part of a triplet and does not exist"; fail=1; continue; \
			fi; \
			out="$$tmp/$$(echo "$$d" | tr / _)"; \
			$(call names_of,$$d,$$out); \
			if [ -z "$$ref" ]; then ref="$$out"; refd="$$d"; continue; fi; \
			missing=$$(comm -23 "$$ref" "$$out"); \
			extra=$$(comm -13 "$$ref" "$$out"); \
			if [ -n "$$missing" ] || [ -n "$$extra" ]; then \
				echo "$$refd and $$d do not carry the same test names. A test that only makes"; \
				echo "sense for one binding belongs in its routing_test.go or binding_test.go, and the"; \
				echo "difference it pins belongs in FL-013:"; \
				[ -n "$$missing" ] && echo "$$missing" | sed "s|^|  only in $$refd: |"; \
				[ -n "$$extra" ]   && echo "$$extra"   | sed "s|^|  only in $$d: |"; \
				fail=1; \
			fi; \
		done; \
	done; \
	if [ $$fail -ne 0 ]; then exit 1; fi; \
	echo "check-triplets: ok"

# A placeholder directory is invisible to go build, vet, test, list and
# gofmt — which is what makes it free, and also what means nothing notices
# when it outlives its purpose. This is the thing that notices.
check-todo: ## A directory holds a TODO.md or Go files, never both
	@bad=$$(find . -name TODO.md -not -path './.git/*' -printf '%h\n' 2>/dev/null | while read -r d; do \
		ls "$$d"/*.go >/dev/null 2>&1 && echo "$$d"; done); \
	if [ -n "$$bad" ]; then \
		echo "TODO.md left beside real code — delete it in the change that added the code:"; \
		echo "$$bad" | sed 's/^/  /'; exit 1; \
	fi; \
	echo "check-todo: ok"

# Every go.mod matches the imports of its own module. This is the half of the
# consumer's view that can be checked before the first tag; the other half —
# that a satellite builds standalone — needs a go.sum hash only a published
# version can mint, and is waited for rather than faked.
check-tidy: ## Every go.mod is tidy
	@fail=0; \
	for m in $(SATELLITES); do \
		root=$$(realpath --relative-to="$$m" .); \
		out=$$(cd $$m \
			&& GOWORK=off $(GO) mod edit -replace $(MOD)=$$root \
			&& GOWORK=off $(GO) mod tidy -diff 2>&1; \
			GOWORK=off $(GO) mod edit -dropreplace $(MOD)); \
		if [ -n "$$out" ]; then echo "$$m is not tidy — run make tidy"; fail=1; fi; \
	done; \
	for m in . $(UNPUBLISHED); do \
		out=$$(cd $$m && GOWORK=off $(GO) mod tidy -diff 2>&1); \
		if [ -n "$$out" ]; then echo "$$m is not tidy — run make tidy"; fail=1; fi; \
	done; \
	if [ $$fail -ne 0 ]; then exit 1; fi; \
	echo "check-tidy: ok"

# ---------------------------------------------------------------------------

# `go work use -r .` would pull in _examples, which is deliberately outside
# the workspace: it demonstrates every stack at once and would drag every
# driver and ORM into the build list.
work: ## Regenerate go.work from the modules on disk
	@rm -f go.work
	@$(GO) work init
	@for m in $(MODULES) ./test; do $(GO) work use $$m; done
	@echo "go.work: $$($(GO) work edit -json | grep -c '"DiskPath"') modules"

# Any module here that requires a sibling resolves it the way a consumer would,
# so until the first tag exists `go mod tidy` cannot see it. That is not only the
# satellites: the moment the root requires a first-party module (D-036), the root
# has the same problem. So every module gets the same treatment — a transient
# replace for each sibling it requires, added and dropped around the tidy, so
# nothing survives in a published go.mod. D-033 forbids that, because a replace
# is invisible to consumers and hides the fact that the version does not exist.
#
# The path is recorded before the edit and the drops run unconditionally: an
# earlier version bailed out mid-loop on a failed edit and left the replaces it
# had already added sitting in a published go.mod — a silent D-033 violation,
# which is the one failure this target must not have.
#
# The replace goes on every sibling, not only the ones this go.mod names: a
# satellite reaches `errs` through the root, so requiring it directly is not the
# test. A replace for a module nothing needs is inert and is dropped either way.
#
# A sibling that already carries a permanent replace — test/ and _examples/ have
# them by design — is left alone.
#
# This fixes go.mod. It cannot mint a go.sum entry, which only a published
# version can, so `GOWORK=off go build` in a satellite stays broken until
# the first tag. That half is not worked around; it is waited for.
tidy: ## Tidy every module
	@for m in $(ALL_MODULES); do \
		echo "==> $$m"; \
		( cd $$m || exit 1; \
		  added=""; rc=0; \
		  for o in $(ALL_MODULES); do \
			[ "$$o" = "$$m" ] && continue; \
			case "$$o" in .) op="$(MOD)";; *) op="$(MOD)/$${o#./}";; esac; \
			grep -q "^replace $$op " go.mod && continue; \
			added="$$added $$op"; \
			GOWORK=off $(GO) mod edit -replace "$$op=$(CURDIR)/$${o#./}" || rc=1; \
		  done; \
		  [ $$rc -eq 0 ] && { GOWORK=off $(GO) mod tidy || rc=1; }; \
		  for op in $$added; do GOWORK=off $(GO) mod edit -dropreplace "$$op"; done; \
		  exit $$rc \
		) || exit 1; \
	done

# The corpus is captured, not written. Every entry is a real driver error from a
# real server, because the engine matrix this replaced was written from memory
# and half of it was wrong — MySQL reports a failed CHECK as 3819/HY000, which
# no reading of the SQLSTATE specification would have predicted.
#
# It needs the servers, so it is not part of `generate`: a target that silently
# produced an empty corpus when the containers were down would be worse than one
# that cannot run at all.
corpus: up ## Recapture the SQL error corpus from the live servers
	cd test && $(GO) run ./cmd/corpus

generate: ## Regenerate the update DTOs and metamodels
	$(GO) generate ./...
	cd test/entstore    && $(GO) generate ./...
	cd test/gormstore   && $(GO) generate ./...
	cd test/versionstore && $(GO) generate ./...
	cd _examples && GOWORK=off $(GO) generate ./...

ent: ## Regenerate the ent code used by the integration tests
	cd test && GOWORK=off $(GO) run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/execquery ./ent/schema

# It tidies afterwards, and that is not tidiness.
#
# `go mod edit -require` writes the line and nothing else, so every satellite is
# left with a go.mod the toolchain refuses — "updates to go.mod needed" — and
# every subsequent build, test and check fails until somebody runs tidy. Since
# `release` begins by running the tests, the documented sequence could not
# complete: it aborted on the first module, with an error about go.mod rather
# than about the release. Rehearsing the release is what found that.
version: ## Point every satellite at a library version: make version V=v0.1.0
	@test -n "$(V)" || (echo "usage: make version V=v0.1.0" && exit 1)
	@for m in $(SATELLITES); do \
		(cd $$m && $(GO) mod edit -require=$(MOD)@$(V)) || exit 1; \
		echo "$$m -> $(V)"; \
	done
	@$(MAKE) --no-print-directory tidy

# `check` as well as `test`: the structural checks are the half that notices a
# module whose go.mod no longer matches its imports, which is exactly the state a
# half-finished version bump leaves behind.
release: test check vuln ## Tag a release: make release V=v0.1.0
	@test -n "$(V)" || (echo "usage: make release V=v0.1.0" && exit 1)
	@git diff --quiet || (echo "working tree is dirty" && exit 1)
	# Every satellite must already require this version of the library — run
	# `make version V=$(V)` and commit before releasing, or a consumer resolves
	# the binding against a library it was never tested with.
	@for m in $(SATELLITES); do \
		grep -qE "$(MOD) $(V)( //.*)?$$" $$m/go.mod || \
			(echo "$$m/go.mod does not require the library at $(V); run make version V=$(V)" && exit 1); \
	done
	# The library is tagged first: the satellite tags carry go.mod files that
	# name it, and a tag that points at a version nobody can fetch is worse
	# than no tag at all. Tag creation is idempotent and the push is atomic,
	# because a release that fails halfway leaves immutable tags behind — the
	# proxy and the checksum database do not forget.
	@tags="$(V)"; \
	for m in $(SATELLITES); do tags="$$tags $${m#./}/$(V)"; done; \
	for t in $$tags; do \
		git rev-parse -q --verify "refs/tags/$$t" >/dev/null || git tag -a "$$t" -m "$$t" || exit 1; \
	done; \
	git push origin --atomic $$tags
	@echo "consumers:"
	@echo "  go get $(MOD)@$(V)"
	@for m in $(SATELLITES); do echo "  go get $(MOD)/$${m#./}@$(V)"; done

clean: down ## Stop the databases and clear the test cache
	$(GO) clean -testcache
