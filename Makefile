.PHONY: help test unit integration examples up down logs psql mysql mariadb fmt vet tidy generate ent version release clean \
        work check check-deps check-tiers check-todo check-tidy

GO  ?= go
MOD := github.com/shardit-io/vv

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
TIER0 := crud query errs port

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

unit: ## Run the unit tests of every module (no database needed)
	@for m in $(WORKSPACE); do echo "==> $$m"; (cd $$m && $(GO) test ./...) || exit 1; done
	@echo "==> ./_examples"; cd _examples && GOWORK=off $(GO) test ./...

integration: up ## Run the integration suite against Postgres and MySQL
	$(GO) test -tags=integration -count=1 ./test/...

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

check: check-deps check-tiers check-todo check-tidy ## Every structural check

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

check-tiers: ## A contract package imports only stdlib and other contract packages
	@fail=0; re=$$(echo "$(TIER0)" | tr ' ' '|'); \
	for p in $(TIER0); do \
		[ -d "$$p" ] || continue; \
		bad=$$($(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./$$p/... 2>/dev/null \
		      | grep "^$(MOD)/" | sed 's|^$(MOD)/||' \
		      | grep -Ev "^($$re)(/|$$)" || true); \
		if [ -n "$$bad" ]; then \
			echo "contract package $$p imports non-contract packages:"; \
			echo "$$bad" | sort -u | sed 's/^/  /'; fail=1; \
		fi; \
	done; \
	if [ $$fail -ne 0 ]; then exit 1; fi; \
	echo "check-tiers: ok"

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

# A published satellite resolves the library the way a consumer does, so
# until the first tag exists `go mod tidy` cannot see it. A transient
# replace is added and dropped around the tidy, so nothing survives in a
# published go.mod — D-033 forbids that, because a replace is invisible to
# consumers and hides the fact that the version does not exist yet.
#
# This fixes go.mod. It cannot mint a go.sum entry, which only a published
# version can, so `GOWORK=off go build` in a satellite stays broken until
# the first tag. That half is not worked around; it is waited for.
tidy: ## Tidy every module
	@echo "==> ."
	@GOWORK=off $(GO) mod tidy
	@for m in $(SATELLITES); do \
		echo "==> $$m"; \
		root=$$(realpath --relative-to="$$m" .); \
		(cd $$m \
			&& GOWORK=off $(GO) mod edit -replace $(MOD)=$$root \
			&& GOWORK=off $(GO) mod tidy \
			&& GOWORK=off $(GO) mod edit -dropreplace $(MOD)) || exit 1; \
	done
	# test/ and _examples/ carry permanent replaces by design and tidy as they are.
	@for m in $(UNPUBLISHED); do echo "==> $$m"; (cd $$m && GOWORK=off $(GO) mod tidy) || exit 1; done

generate: ## Regenerate the update DTOs and metamodels
	$(GO) generate ./...
	cd test/entstore  && $(GO) generate ./...
	cd test/gormstore && $(GO) generate ./...
	cd _examples && GOWORK=off $(GO) generate ./...

ent: ## Regenerate the ent code used by the integration tests
	cd test && GOWORK=off $(GO) run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/execquery ./ent/schema

version: ## Point every satellite at a library version: make version V=v0.1.0
	@test -n "$(V)" || (echo "usage: make version V=v0.1.0" && exit 1)
	@for m in $(SATELLITES); do \
		(cd $$m && $(GO) mod edit -require=$(MOD)@$(V)) || exit 1; \
		echo "$$m -> $(V)"; \
	done

release: test ## Tag a release: make release V=v0.1.0
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
