# Database migrations

Numbered SQL migrations applied by `scripts/migrate.sh`. PostgreSQL is the
source of truth for durable state; the Go API never creates or alters schema
on its own — it only verifies at startup (and via `/readyz`) that the schema
is at the version it expects.

## Conventions

- File name: `NNNN_name.sql` with a zero-padded, strictly increasing version
  number. Versions are never reused or edited after being applied; changes are
  new migrations.
- Every migration runs in a single transaction together with its
  `schema_migrations` record. A failed migration rolls back completely and the
  runner exits non-zero with the failing file named.
- `schema_migrations` (created by the runner, not by a migration file) records
  `version`, `name`, `checksum`, and `applied_at`. Rerunning the runner skips
  applied versions; a changed checksum for an applied version is reported as a
  warning.
- Extensions go in migrations too (`0001` enables `vector`; UUID generation
  uses core `gen_random_uuid()`, available since PostgreSQL 13).

## Usage

```sh
sh scripts/migrate.sh          # apply pending migrations (compose postgres)
sh scripts/migrate.sh status   # list applied/pending migrations
sh scripts/migrate.sh --container NAME   # target a specific postgres container
```

The default target is the running compose `postgres` service; no host `psql`
is required. Credentials come from `POSTGRES_USER`/`POSTGRES_PASSWORD`/
`POSTGRES_DB` (defaults `ragbench`/`ragbench`/`ragbench`).

The Go binary pins the version it expects in
`backend/internal/schema/schema.go` (`RequiredVersion`). Adding a migration
requires bumping that constant in the same change.
