GO ?= go
export GO V

COMMANDS := unit integration test examples up down logs psql mysql mariadb fmt vet \
	check check-deps check-tiers check-utils check-triplets check-todo \
	check-replaces check-tidy \
	tidy main-deps corpus generate ent api vuln version release clean

.PHONY: help $(COMMANDS)
.DEFAULT_GOAL := help

help:
	@./scripts/vv help

$(COMMANDS):
	@./scripts/vv $@
