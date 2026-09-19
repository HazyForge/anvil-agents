package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestKindChatPostgresURLShapeLoadable pins the disposable-Postgres URL
// contract emitted by hack/kind-chat-postgres.sh: the exact strings the
// helper prints must parse through pgxpool.ParseConfig, the same driver
// entry point OpenPostgresStore uses before dialing. No network, no Docker,
// no database: parse only. Live migration/upsert stays covered by
// TestPostgresStoreIntegration via hack/test-archive-postgres.sh.
func TestKindChatPostgresURLShapeLoadable(t *testing.T) {
	good := map[string]struct {
		host     string
		database string
		user     string
	}{
		"postgresql://anvil_agents:anvil-kind-chat-dev-only@127.0.0.1:54329/anvil_agents?sslmode=disable": {
			host: "127.0.0.1", database: "anvil_agents", user: "anvil_agents",
		},
		"postgresql://anvil_agents:pw@localhost:5432/anvil_agents?sslmode=disable": {
			host: "localhost", database: "anvil_agents", user: "anvil_agents",
		},
		"postgresql://anvil_agents:pw@[::1]:5432/anvil_agents?sslmode=disable": {
			host: "::1", database: "anvil_agents", user: "anvil_agents",
		},
		"postgresql://custom:secret@127.0.0.1:5432/customdb?sslmode=disable": {
			host: "127.0.0.1", database: "customdb", user: "custom",
		},
	}
	for url, want := range good {
		config, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatalf("ParseConfig(%q) failed: %v", url, err)
		}
		if config.ConnConfig.Host != want.host {
			t.Fatalf("ParseConfig(%q) host = %q, want %q", url, config.ConnConfig.Host, want.host)
		}
		if config.ConnConfig.Database != want.database {
			t.Fatalf("ParseConfig(%q) database = %q, want %q", url, config.ConnConfig.Database, want.database)
		}
		if config.ConnConfig.User != want.user {
			t.Fatalf("ParseConfig(%q) user = %q, want %q", url, config.ConnConfig.User, want.user)
		}
	}

	// pgxpool.ParseConfig is deliberately lenient (empty strings and
	// missing user/database parse without error), so the negative shape
	// contract — loopback host, userinfo, database path, sslmode=disable —
	// lives in `hack/kind-chat-postgres.sh --check-url`, covered by
	// hack/kind-chat-postgres_test.sh. Here only truly unparsable input
	// must fail; everything else is pinned by field assertions above.
	for _, bad := range []string{
		"not-a-url",
		"://broken",
	} {
		if _, err := pgxpool.ParseConfig(bad); err == nil {
			t.Fatalf("ParseConfig(%q) succeeded, want error", bad)
		}
	}
}

// TestKindChatPostgresURLRequired documents that OpenPostgresStore rejects an
// empty URL before touching the network: Kind-local bring-up that forgets the
// helper's export fails fast with guidance instead of hanging on connect.
func TestKindChatPostgresURLRequired(t *testing.T) {
	ctx := context.Background()
	if _, err := OpenPostgresStore(ctx, ""); err == nil {
		t.Fatal("OpenPostgresStore with empty URL succeeded, want error")
	} else if got := err.Error(); got != "database URL is required" {
		t.Fatalf("OpenPostgresStore empty-URL error = %q, want database-URL-required guidance", got)
	}
	if _, err := OpenPostgresStore(ctx, "://broken"); err == nil {
		t.Fatal("OpenPostgresStore with malformed URL succeeded, want error")
	} else if !strings.HasPrefix(err.Error(), "parse standing-chat database URL: ") {
		t.Fatalf("OpenPostgresStore malformed-URL error = %q, want parse guidance", err.Error())
	}
}
