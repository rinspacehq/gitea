# Rinspace editor catalog

`GET /api/v1/user/rinspace-editor/projects` is an opt-in, metadata-only endpoint.
Enable `[api] RINSPACE_EDITOR_CATALOG_ENABLED = true` only after the PostgreSQL
catalog migration and integration checks pass. It defaults to disabled. Other
database engines and missing/invalid indexes return an explicit unavailable
response; there is no unindexed or upstream-page-scanning fallback.

The route requires the existing authenticated `/user` API context. A personal
access token needs both `read:user` and `read:repository` scopes (or their write
equivalents). The actor comes from that context, never an actor query parameter.
Public-only token restrictions remain enforced. GET never grants collaboration
rights. The response is not the general repository converter and does not load
Git objects or source archives.

Parameters are `q` (at most 200 UTF-8 bytes, literal case-insensitive contains),
`limit` (1–50, default 24) and optional canonical positive `before_id`. An exact
`owner/name` query uses the owner/name lookup. Repository IDs are descending;
pass the previous response's `nextCursor` as `before_id`. Do not compute offsets
or rely on legacy total-count headers. Refresh/search changes start without a
cursor. Newer IDs appear on refresh, while every request rechecks current ACLs.

The response schema is `rinspace-gitea-editor-catalog/v1`, with `account`,
`projects`, `limit`, `query`, `hasMore`, and optional `nextCursor`.
The project entries contain repository identity, presentation, default branch,
object format and actual Code-unit permissions. A concurrent permission change
fails the page rather than leaking data or silently skipping entries.

The default database query excludes both the canonical tags namespace and tag
type projections before limiting. Search adds visible public, nonarchived tags
outside the actor's memberships, while keeping ordinary projects account-scoped.
Candidates use PostgreSQL's built-in GIN array operators and a versioned
1/2/3-character token function followed by an exact literal substring check.
No external search service or `pg_trgm` extension is needed by the implementation.

Migration 349 adds versioned immutable functions and six indexes. Published
function versions must not be edited in-place. Build/rebuild is an operation,
never an HTTP side effect. Budget the function/index build, WAL, scratch space
and backup/restore before deployment; a previous application image may reject a
new database schema version, so retaining that image alone is not a rollback.
Use PostgreSQL-native backup/restore that preserves the functions and indexes;
an ORM-only schema export is not a complete catalog backup.
Fresh PostgreSQL installations initialize these expression indexes before
recording the current schema version; ordinary model synchronization alone
cannot create them. Request handlers never create or repair indexes.
The PostgreSQL engine uses XORM's dialect extension to keep only the six exact
migration-owned index/table/column layouts out of XORM's model-index metadata.
XORM 1.3.11 otherwise rejects expression functions as missing entity columns or
drops these unmodeled indexes during schema synchronization. The real indexes
stay in PostgreSQL and remain subject to the API's independent readiness check;
all unrelated index layouts and metadata errors are preserved. No third-party
module or query/index semantics are changed.

The optional `rinspace_catalog_postgres` Go build tag enables an isolated model
suite. It **fails** unless `RINSPACE_CATALOG_TEST_DSN` explicitly points to a
disposable database named `editor_catalog_model_test`. It creates a fresh test
schema and refuses to overwrite one. It exercises the actual model query and
metadata loading at 10,000 and 100,000 synthetic tags. Set
`RINSPACE_CATALOG_REPORT_DIR` to a dedicated writable report directory to retain
the EXPLAIN JSON. Run it without production credentials, mounts or network.

The same build tag enables `TestAPIRinspaceEditorCatalog` in `tests/integration`.
It runs the installed router and real token authentication against a disposable
PostgreSQL database, covering scopes, owner/collaborator/Code-team permissions,
organization visibility, public-only tokens, hidden/default and global/search
tags, keyset pagination, input/feature gates, and fail-closed missing indexes.
The fixture repositories have no Git objects, enforcing metadata-only loading.
Search checks that no collaboration or access row is created.

Use the `rinspace-catalog-pg` integration profile with `TEST_PGSQL_HOST` pointing
only at that disposable PostgreSQL instance. Integration setup **drops and
recreates** `editor_catalog_api_test`; it must never reach an application database.
The profile uses local storage and does not require external object storage:

```sh
GITEA_TEST_DATABASE=rinspace-catalog-pg TEST_PGSQL_HOST=/isolated/pgsocket \
  go test -p 1 -tags rinspace_catalog_postgres ./tests/integration \
  -run '^TestAPIRinspaceEditorCatalog$'
```

Run in isolated filesystem/network namespaces with no application credentials or
runtime mounts. These checks do not replace cold-disk performance tests,
migration backup/restore rehearsal, or private same-artifact consumption.
