// Package schema verifies that the application database has been migrated to
// the schema version this binary expects. It never creates or alters schema:
// migrations are applied out of band with scripts/migrate.sh, so the API can
// not silently bring up an alternative schema.
package schema

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RequiredVersion is the highest migration version under db/migrations/ that
// this binary is built against. It must be bumped whenever a migration is
// added; the readiness check fails until the database is migrated to it.
const RequiredVersion = 6

// ErrNotMigrated reports that the migration tracking table is missing, which
// means no migrations have been applied to this database.
var ErrNotMigrated = errors.New("application schema is not migrated: schema_migrations table is missing; run sh scripts/migrate.sh")

// OutOfDateError reports a schema version mismatch between the database and
// this binary. Applied below RequiredVersion means migrations must be run;
// above it means the database is newer than the running binary.
type OutOfDateError struct {
	Applied  int
	Required int
}

func (e *OutOfDateError) Error() string {
	if e.Applied < e.Required {
		return fmt.Sprintf(
			"application schema is out of date: applied migration version %d, expected %d; run sh scripts/migrate.sh",
			e.Applied, e.Required)
	}
	return fmt.Sprintf(
		"application schema is newer than this binary: applied migration version %d, expected %d",
		e.Applied, e.Required)
}

// Check returns nil when the database schema is at RequiredVersion, and a
// structured error otherwise. It is suitable as a readiness check: readiness
// must not report ready against an unmigrated or mismatched database.
func Check(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name = 'schema_migrations'
		)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check schema_migrations table: %w", err)
	}

	if !exists {
		return ErrNotMigrated
	}

	var applied int
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
	).Scan(&applied); err != nil {
		return fmt.Errorf("read applied schema version: %w", err)
	}

	return validateState(exists, applied)
}

// validateState maps observed schema state to a structured result. It is
// separated from the database access so the core rule is unit-testable.
func validateState(trackingTableExists bool, appliedVersion int) error {
	if !trackingTableExists {
		return ErrNotMigrated
	}
	if appliedVersion != RequiredVersion {
		return &OutOfDateError{Applied: appliedVersion, Required: RequiredVersion}
	}
	return nil
}
