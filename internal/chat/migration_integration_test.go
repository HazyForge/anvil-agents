package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only within the disposable PostgreSQL integration fixture. The role has
// no database CREATE privilege; an administrator provisions its schema once.
func exercisePreprovisionedSchema(t *testing.T, fixture *PostgresStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	database := "chat_migration_" + suffix
	role := "chat_migrator_" + suffix
	dbIdent := pgx.Identifier{database}.Sanitize()
	roleIdent := pgx.Identifier{role}.Sanitize()
	if _, err := fixture.pool.Exec(ctx, "CREATE ROLE "+roleIdent+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = fixture.pool.Exec(context.Background(), "DROP ROLE "+roleIdent) }()
	if _, err := fixture.pool.Exec(ctx, "CREATE DATABASE "+dbIdent); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = fixture.pool.Exec(context.Background(), "DROP DATABASE "+dbIdent+" WITH (FORCE)") }()
	adminConfig := fixture.pool.Config().Copy()
	adminConfig.ConnConfig.Database = database
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA anvil_agents_chat AUTHORIZATION "+roleIdent); err != nil {
		t.Fatal(err)
	}
	restrictedConfig := adminConfig.Copy()
	restrictedConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+roleIdent)
		return err
	}
	restricted, err := pgxpool.NewWithConfig(ctx, restrictedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer restricted.Close()
	var broadCreate bool
	if err = restricted.QueryRow(ctx, "SELECT has_database_privilege(current_user,current_database(),'CREATE')").Scan(&broadCreate); err != nil {
		t.Fatal(err)
	}
	if broadCreate {
		t.Fatal("fixture role unexpectedly has database CREATE")
	}
	store := &PostgresStore{pool: restricted}
	for i := 0; i < 2; i++ {
		if err = store.Migrate(ctx); err != nil {
			t.Fatalf("schema-owned migration %d: %v", i, err)
		}
	}
	exerciseTurnOutbox(t, store)
	if err = restricted.QueryRow(ctx, "SELECT has_database_privilege(current_user,current_database(),'CREATE')").Scan(&broadCreate); err != nil {
		t.Fatal(err)
	}
	if broadCreate {
		t.Fatal("migration widened database privileges")
	}
}
