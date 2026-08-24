.PHONY: help test unit integration examples up down logs psql mysql fmt vet lint tidy generate ent version release clean

GO ?= go

# Every published module. The library is `.`; the others are the bindings and
# adapters that carry an external dependency of their own, kept separate so a
# consumer takes only the ones it imports (D-033). `test/` is not published and
# is handled on its own line wherever it matters.
MODULES := . adapter/crudpgx http/crudfiber http/crudgin

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

unit: ## Run the unit tests of every module (no database needed)
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && $(GO) test ./...) || exit 1; done

integration: up ## Run the integration suite against Postgres and MySQL
	$(GO) test -tags=integration -count=1 ./test/...

test: unit integration ## Everything

examples: ## Build and vet the runnable examples (needs the databases to run them)
	cd _examples && GOWORK=off $(GO) build ./... && GOWORK=off $(GO) vet ./... && GOWORK=off $(GO) test ./...

up: ## Start Postgres and MySQL and wait for them
	docker compose up -d --wait

down: ## Stop and remove the databases
	docker compose down -v

logs: ## Tail the database logs
	docker compose logs -f

psql: ## Open a psql shell
	docker compose exec postgres psql -U rxcrud -d rxcrud

mysql: ## Open a mysql shell
	docker compose exec mysql mysql -urxcrud -prxcrud rxcrud

fmt: ## gofmt everything
	gofmt -l -w crud repo query http cmd adapter test _examples

vet: ## go vet every module, including the test module
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && $(GO) vet ./...) || exit 1; done
	$(GO) vet -tags=integration ./test/...

tidy: ## Tidy every module
	# GOWORK=off because `go mod tidy` ignores the workspace by design: each
	# module has to resolve the library the way a consumer would, which means
	# the version its go.mod names must be a tag that exists.
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		(cd $$m && GOWORK=off $(GO) mod tidy) || { \
			echo ""; \
			echo "If that failed on an unknown revision of the library: a submodule"; \
			echo "resolves it the way a consumer does, so the version its go.mod"; \
			echo "names has to be a pushed tag. Building and testing work without"; \
			echo "one — go.work covers them — but tidying does not. Cut a tag:"; \
			echo "    git tag -a v0.0.1 -m v0.0.1 && git push origin v0.0.1"; \
			echo "    make version V=v0.0.1"; \
			exit 1; }; \
	done
	cd test && GOWORK=off $(GO) mod tidy
	cd _examples && GOWORK=off $(GO) mod tidy

generate: ## Regenerate the update DTOs and metamodels
	$(GO) generate ./...
	cd test/entstore  && $(GO) generate ./...
	cd test/gormstore && $(GO) generate ./...
	cd _examples && GOWORK=off $(GO) generate ./...

ent: ## Regenerate the ent code used by the integration tests
	cd test && GOWORK=off $(GO) run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/execquery ./ent/schema

version: ## Point every submodule at a library version: make version V=v0.1.0
	@test -n "$(V)" || (echo "usage: make version V=v0.1.0" && exit 1)
	@for m in $(MODULES); do \
		test "$$m" = "." && continue; \
		(cd $$m && $(GO) mod edit -require=github.com/shardit-io/rx@$(V)) || exit 1; \
		echo "$$m -> $(V)"; \
	done

release: test ## Tag a release: make release V=v0.1.0
	@test -n "$(V)" || (echo "usage: make release V=v0.1.0" && exit 1)
	@git diff --quiet || (echo "working tree is dirty" && exit 1)
	# Every submodule must already require this version of the library — run
	# `make version V=$(V)` and commit before releasing, or a consumer resolves
	# the binding against a library it was never tested with.
	@for m in $(MODULES); do \
		test "$$m" = "." && continue; \
		grep -q "go-rx-crud $(V)$$" $$m/go.mod || \
			(echo "$$m/go.mod does not require the library at $(V); run make version V=$(V)" && exit 1); \
	done
	# The library is tagged first: the submodule tags carry go.mod files that
	# name it, and a tag that points at a version nobody can fetch is worse
	# than no tag at all.
	git tag -a $(V) -m "$(V)"
	git push origin $(V)
	@for m in $(MODULES); do \
		test "$$m" = "." && continue; \
		git tag -a "$$m/$(V)" -m "$$m/$(V)" && git push origin "$$m/$(V)" || exit 1; \
	done
	@echo "consumers:"
	@echo "  go get github.com/shardit-io/rx@$(V)"
	@for m in $(MODULES); do \
		test "$$m" = "." && continue; \
		echo "  go get github.com/shardit-io/rx/$$m@$(V)"; \
	done

clean: down ## Stop the databases and clear the test cache
	$(GO) clean -testcache
