#!/usr/bin/env sh
set -eu

# Applies numbered SQL migrations from db/migrations/ to the application
# PostgreSQL database.
#
# Idempotent: applied migrations are recorded in schema_migrations and skipped
# on rerun. Each migration runs in a single transaction together with its
# schema_migrations record, so a failed migration leaves no partial state and
# the failure is reported on stderr with a non-zero exit code.
#
# Usage:
#   sh scripts/migrate.sh                    apply pending migrations
#   sh scripts/migrate.sh status             list applied/pending migrations
#   sh scripts/migrate.sh --container NAME   target a specific postgres container
#
# The runner needs a running postgres container. By default it targets the
# compose `postgres` service and connects inside that container, so no host
# psql client is required.
#
# Environment:
#   POSTGRES_USER / POSTGRES_PASSWORD / POSTGRES_DB   defaults: ragbench ×3

cd "$(dirname "$0")/.."

MIGRATIONS_DIR="db/migrations"
POSTGRES_USER="${POSTGRES_USER:-ragbench}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-ragbench}"
POSTGRES_DB="${POSTGRES_DB:-ragbench}"

container=""
subcommand="up"
while [ $# -gt 0 ]; do
    case "$1" in
        --container)
            [ $# -ge 2 ] || { echo "error: --container requires a value" >&2; exit 2; }
            container="$2"
            shift 2
            ;;
        status)
            subcommand="status"
            shift
            ;;
        *)
            echo "error: unknown argument '$1'" >&2
            echo "usage: sh scripts/migrate.sh [--container NAME] [status]" >&2
            exit 2
            ;;
    esac
done

command -v docker >/dev/null 2>&1 || {
    echo "error: docker is required to run migrations" >&2
    exit 1
}

if [ -z "$container" ]; then
    container=$(docker compose ps -q postgres 2>/dev/null || true)
    if [ -z "$container" ]; then
        echo "error: postgres service is not running; start the stack with 'sh scripts/dev.sh'" >&2
        exit 1
    fi
fi

psql() {
    # Local-socket connections inside the official postgres image are trusted,
    # so no password is needed here.
    docker exec -i "$container" psql -v ON_ERROR_STOP=1 \
        -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"
}

# sha256 of a file, portable across macOS (shasum) and Linux (sha256sum).
checksum() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

migration_files() {
    # Numbered files only; lexicographic order matches numeric order because
    # every file name uses a fixed-width zero-padded version prefix.
    ls "$MIGRATIONS_DIR" 2>/dev/null | grep -E '^[0-9]{4}_[a-z0-9_]+\.sql$' | sort || true
}

version_of() {
    printf '%s' "$1" | cut -d_ -f1 | sed 's/^0*//'
}

ensure_tracking_table() {
    psql -q <<-'SQL'
        SET client_min_messages = warning;
        CREATE TABLE IF NOT EXISTS schema_migrations (
            version INTEGER PRIMARY KEY,
            name TEXT NOT NULL,
            checksum TEXT NOT NULL,
            applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
        );
SQL
}

applied_versions() {
    psql -t -A -c "SELECT version || ':' || checksum FROM schema_migrations ORDER BY version;"
}

warn_if_no_migrations() {
    if [ -z "$(migration_files)" ]; then
        echo "error: no numbered migration files found in $MIGRATIONS_DIR" >&2
        exit 1
    fi
}

show_status() {
    warn_if_no_migrations
    state=$(applied_versions)
    printf '%-8s %-10s %s\n' VERSION STATUS NAME
    for file in $(migration_files); do
        version=$(version_of "$file")
        if printf '%s\n' "$state" | grep -q "^${version}:"; then
            status=applied
        else
            status=pending
        fi
        printf '%-8s %-10s %s\n' "$version" "$status" "$file"
    done
}

apply_pending() {
    warn_if_no_migrations
    ensure_tracking_table
    state=$(applied_versions)

    applied_any=0
    for file in $(migration_files); do
        version=$(version_of "$file")
        path="$MIGRATIONS_DIR/$file"
        sum=$(checksum "$path")

        if printf '%s\n' "$state" | grep -q "^${version}:"; then
            recorded=$(printf '%s\n' "$state" | grep "^${version}:" | cut -d: -f2)
            if [ "$recorded" != "$sum" ]; then
                echo "warning: migration $file changed since it was applied (checksum drift); leaving recorded state untouched" >&2
            fi
            continue
        fi

        applied_any=1
        echo "applying migration $version ($file)"
        # pg_advisory_xact_lock serializes concurrent runners; the whole stdin
        # script (migration + bookkeeping) commits or rolls back as one unit.
        {
            printf 'SELECT pg_advisory_xact_lock(4206042) AS _migration_lock \\gset\n'
            cat "$path"
            printf "\nINSERT INTO schema_migrations (version, name, checksum) VALUES (%s, '%s', '%s');\n" \
                "$version" "$file" "$sum"
        } | psql --single-transaction -q || {
            echo "error: migration $version ($file) failed; the transaction was rolled back and no schema_migrations record was written" >&2
            exit 1
        }
    done

    if [ "$applied_any" -eq 0 ]; then
        echo "database schema is up to date; nothing to apply"
    else
        echo "migrations applied successfully"
    fi
}

case "$subcommand" in
    status) show_status ;;
    up) apply_pending ;;
esac
