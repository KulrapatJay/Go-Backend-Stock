package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const insufficientPrivilege = "42501"

func openPool(t *testing.T, key string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), integrationURL(t, key))
	if err != nil {
		t.Fatalf("connect %s: %v", key, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestBootstrapAndBaselineMigration(t *testing.T) {
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	ctx := t.Context()

	var appTable, publicTable *string
	if err := admin.QueryRow(ctx,
		`SELECT to_regclass('app.goose_db_version')::text, to_regclass('public.goose_db_version')::text`,
	).Scan(&appTable, &publicTable); err != nil {
		t.Fatalf("query goose tables: %v", err)
	}
	if appTable == nil {
		t.Error("app.goose_db_version does not exist: goose did not use the schema-qualified table")
	}
	if publicTable != nil {
		t.Error("public.goose_db_version exists: nothing may be created in public")
	}

	var schemaOwner string
	if err := admin.QueryRow(ctx,
		`SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'app'`,
	).Scan(&schemaOwner); err != nil {
		t.Fatalf("query schema owner: %v", err)
	}
	if schemaOwner != "app_migrator" {
		t.Errorf("schema app owner = %q, want app_migrator", schemaOwner)
	}

	var baselineApplied bool
	if err := admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM app.goose_db_version WHERE version_id = 1 AND is_applied)`,
	).Scan(&baselineApplied); err != nil {
		t.Fatalf("query baseline version: %v", err)
	}
	if !baselineApplied {
		t.Error("baseline migration 00001 is not recorded as applied")
	}

	var publicObjects int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND pg_get_userbyid(c.relowner) IN ('app_migrator', 'app_runtime', 'app_maintenance')`,
	).Scan(&publicObjects); err != nil {
		t.Fatalf("query public objects: %v", err)
	}
	if publicObjects != 0 {
		t.Errorf("app roles own %d objects in public, want 0", publicObjects)
	}

	for _, role := range []string{"app_runtime", "app_maintenance"} {
		var super, bypass, createDB, createRole bool
		if err := admin.QueryRow(ctx,
			`SELECT rolsuper, rolbypassrls, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = $1`, role,
		).Scan(&super, &bypass, &createDB, &createRole); err != nil {
			t.Fatalf("query role %s: %v", role, err)
		}
		if super || bypass || createDB || createRole {
			t.Errorf("%s has elevated attributes: super=%v bypassrls=%v createdb=%v createrole=%v",
				role, super, bypass, createDB, createRole)
		}
	}
}

func TestRuntimeRoleCannotChangeSchema(t *testing.T) {
	runtime := openPool(t, "TEST_RUNTIME_DATABASE_URL")
	statements := map[string]string{
		"create in app":      `CREATE TABLE app.should_not_exist (id int)`,
		"create in public":   `CREATE TABLE public.should_not_exist (id int)`,
		"read goose history": `SELECT count(*) FROM app.goose_db_version`,
	}
	for name, sql := range statements {
		t.Run(name, func(t *testing.T) {
			_, err := runtime.Exec(t.Context(), sql)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != insufficientPrivilege {
				t.Fatalf("Exec(%q) error = %v, want SQLSTATE %s", sql, err, insufficientPrivilege)
			}
		})
	}
}
