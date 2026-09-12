GO ?= go
export GO V

COMMANDS := unit integration test examples up down logs psql mysql mariadb fmt vet \
	check check-deps check-tiers check-utils check-triplets check-todo \
	check-replaces check-tidy \
	check-otel-schema check-otel-module check-otel-live check-otel-native-budget check-otel-operations check-otel-wire check-otel-collector check-otel-prometheus check-otel-weaver check-workspace check-otel-consumer check-i18n-consumer \
	check-event-kernel check-event-kernel-baseline check-event-kernel-moved \
	check-event-consumer check-event-combinations \
	tidy main-deps corpus generate ent api vuln version release clean

.PHONY: help $(COMMANDS)
.DEFAULT_GOAL := help

help:
	@./scripts/vv help

$(COMMANDS):
	@./scripts/vv $@
