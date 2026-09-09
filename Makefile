GO ?= go
export GO V

COMMANDS := unit integration test examples up down logs psql mysql mariadb fmt vet \
	check check-deps check-tiers check-utils check-triplets check-todo \
	check-replaces check-tidy \
<<<<<<< HEAD
	check-otel-schema check-otel-module check-otel-live check-workspace check-otel-consumer check-i18n-consumer \
	check-event-kernel \
=======
	check-otel-schema check-otel-module check-workspace check-otel-consumer \
	check-event-kernel check-event-kernel-baseline check-event-kernel-moved \
>>>>>>> 5fedaae3d4e688198eb0116509c76d075429340e
	tidy main-deps corpus generate ent api vuln version release clean

.PHONY: help $(COMMANDS)
.DEFAULT_GOAL := help

help:
	@./scripts/vv help

$(COMMANDS):
	@./scripts/vv $@
