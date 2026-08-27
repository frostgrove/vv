#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

case ${1:-} in
	up) exec docker compose up -d --wait ;;
	down) exec docker compose down -v ;;
	logs) exec docker compose logs -f ;;
	psql) exec docker compose exec postgres psql -U vv -d vv ;;
	mysql) exec docker compose exec mysql mysql -uvv -pvv vv ;;
	mariadb) exec docker compose exec mariadb mariadb -uvv -pvv vv ;;
	*) echo "unknown database task: ${1:-}" >&2; exit 2 ;;
esac
