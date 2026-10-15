# Editor catalog query-design probes

These scripts explore database-side filtering, literal substring lookup and
keyset pagination using **synthetic, simplified fixtures**. They are not a Gitea
API implementation, schema migration, permission test or production benchmark.

Run only in a disposable PostgreSQL database named `editor_catalog_bench`,
isolated from application data. The first script creates a fresh
`editor_catalog_probe` schema and installs `pg_trgm` **in that disposable DB**
to measure a baseline. The second script operates on that fixture and tests a
GIN array-index alternative which does not itself depend on `pg_trgm`.

```sh
psql -X -qAt -v ON_ERROR_STOP=1 -d editor_catalog_bench -f editor-catalog-bench.sql
psql -X -qAt -v ON_ERROR_STOP=1 -d editor_catalog_bench -f editor-catalog-index-probe.sql
psql -X -qAt -v ON_ERROR_STOP=1 -v probe_scale=10000 -d editor_catalog_bench -f editor-catalog-index-probe.sql
```

The baseline measures 10,000 and 100,000 synthetic tags. The index probe defaults
to the final 100,000-row fixture; its optional 10,000 mode rebuilds **only that
synthetic fixture**. Do not point either script at a production database or copy
its DDL into a production migration without review.

Each performance case emits seven `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` runs.
The substring equivalence check is separate and uses a 1,000-row sample.
Fixture creation, VACUUM and index building warm caches, so these measurements
are not cold-disk results. Full Gitea ACL semantics, scope enforcement, API
latency, migration/rollback, update costs and name-search indexes remain to be
implemented and tested. Retain failed attempts as failed attempts.
