.PHONY: help test unit integration up down logs psql mysql fmt vet lint tidy generate ent release clean

GO ?= go

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

unit: ## Run the unit tests (no database needed)
	$(GO) test ./...

integration: up ## Run the integration suite against Postgres and MySQL
	$(GO) test -tags=integration -count=1 ./test/...

test: unit integration ## Everything

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
	gofmt -l -w crud repo query http cmd adapter example test

vet: ## go vet the library and the test module
	$(GO) vet ./...
	$(GO) vet -tags=integration ./test/...

tidy: ## Tidy both modules
	$(GO) mod tidy
	cd test && GOWORK=off $(GO) mod tidy

generate: ## Regenerate the update DTOs and metamodels
	$(GO) generate ./...
	cd test/entstore  && $(GO) generate ./...
	cd test/gormstore && $(GO) generate ./...

ent: ## Regenerate the ent code used by the integration tests
	cd test && GOWORK=off $(GO) run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/execquery ./ent/schema

release: test ## Tag a release: make release V=v0.1.0
	@test -n "$(V)" || (echo "usage: make release V=v0.1.0" && exit 1)
	@git diff --quiet || (echo "working tree is dirty" && exit 1)
	git tag -a $(V) -m "$(V)"
	git push origin $(V)
	@echo "consumers: go get github.com/shardit-io/go-rx-crud@$(V)"

clean: down ## Stop the databases and clear the test cache
	$(GO) clean -testcache
