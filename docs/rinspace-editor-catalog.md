# Rinspace editor catalog

`GET /api/v1/user/rinspace-editor/projects` is an opt-in, metadata-only endpoint.
Enable `[api] RINSPACE_EDITOR_CATALOG_ENABLED = true` only after the PostgreSQL
catalog migration and integration checks pass. It defaults to disabled. Other
database engines and missing/invalid indexes return an explicit unavailable
response; there is no unindexed or upstream-page-scanning fallback.

The route requires the existing authenticated `/user` API context and repository
token scope. The actor comes from that context, never an actor query parameter.
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

The optional `rinspace_catalog_postgres` Go build tag enables an isolated model
suite. It **fails** unless `RINSPACE_CATALOG_TEST_DSN` explicitly points to a
disposable database named `editor_catalog_model_test`. It creates a fresh test
schema and refuses to overwrite one. It exercises the actual model query and
metadata loading at 10,000 and 100,000 synthetic tags. Set
`RINSPACE_CATALOG_REPORT_DIR` to a dedicated writable report directory to retain
the EXPLAIN JSON. Run it without production credentials, mounts or network.

These tests do not replace full API scope/team integration, cold-disk tests,
migration backup/restore rehearsal, or private same-artifact consumption.
