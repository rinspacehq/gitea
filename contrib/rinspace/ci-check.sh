#!/usr/bin/env bash
# Copyright 2026 The Gitea Authors. All rights reserved.
# SPDX-License-Identifier: MIT
# Shared check commands for Actions and the isolated independent CI worker.
set -euo pipefail
export GOMAXPROCS=1 GOMEMLIMIT=2GiB

case "${1:-}" in
  formatting)
    git diff --check
    format_report=$(mktemp)
    trap 'rm -f "$format_report"' EXIT
    git ls-files -z '*.go' | xargs -0 gofmt -l > "$format_report"
    test ! -s "$format_report" || { cat "$format_report"; exit 1; }
    node --test contrib/rinspace/ci-check.test.mjs contrib/rinspace/github-checks.test.mjs
    ;;
  models-control)
    go test -p 1 -vet=off ./models/rinauth ./models/rincontrol ./services/rincontrol
    ;;
  authentication)
    go test -p 1 -vet=off ./services/auth
    ;;
  repository)
    go test -p 1 -vet=off ./services/repository -run 'Rinspace|Publication'
    ;;
  editor-catalog)
    go test -p 1 ./models/db -run '^TestRinspaceCatalogPostgresIndexMetadata$'
    go test -p 1 ./modules/rinspacecatalog
    go test -p 1 -tags sqlite,sqlite_unlock_notify ./models/repo -run '^TestRinspaceEditorProject(SQLIsBounded|Options)$'
    go test -p 1 -tags sqlite,sqlite_unlock_notify ./routers/api/v1/user -run '^TestRinspaceEditorCatalog'
    go test -c -p 1 -tags sqlite,sqlite_unlock_notify,rinspace_catalog_postgres -o /tmp/editor-catalog-postgres.test ./models/repo
    go test -c -p 1 -tags sqlite,sqlite_unlock_notify,rinspace_catalog_postgres -o /tmp/editor-catalog-api.test ./tests/integration
    ;;
  migration)
    go test -c -o /dev/null ./models/migrations/v1_27
    ;;
  *)
    echo 'Usage: ci-check.sh {formatting|models-control|authentication|repository|editor-catalog|migration}' >&2
    exit 64
    ;;
esac
