package chat

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestPostgresStoreIntegration(t *testing.T) {
	databaseURL := os.Getenv("ANVIL_AGENTS_TEST_CHAT_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("ANVIL_AGENTS_TEST_ARCHIVE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set ANVIL_AGENTS_TEST_CHAT_DATABASE_URL or ANVIL_AGENTS_TEST_ARCHIVE_DATABASE_URL to run the PostgreSQL chat integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL chat store: %v", err)
	}
	defer store.Close()

	namespace := "chat-integration"
	profile := "grok45-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	created, err := store.CreateThread(ctx, Thread{
		Namespace:   namespace,
		ProfileName: profile,
		Mode:        ModePersona,
		CreatedBy:   "user-1",
		Metadata:    json.RawMessage(`{"fixture":true}`),
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = store.pool.Exec(cleanupCtx, `DELETE FROM anvil_agents_chat.threads WHERE id = $1`, created.ID)
	}()

	listed, err := store.ListThreads(ctx, ThreadFilter{Namespace: namespace, ProfileName: profile})
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || listed[0].ProfileName != profile {
		t.Fatalf("list = %#v", listed)
	}

	stored, thread, err := store.AppendMessages(ctx, namespace, created.ID, []Message{
		{Role: RoleUser, Content: "persist this thread"},
		{Role: RoleAssistant, Content: "stub: persist this thread"},
	})
	if err != nil {
		t.Fatalf("append messages: %v", err)
	}
	if len(stored) != 2 || stored[0].Sequence != 1 || stored[1].Role != RoleAssistant {
		t.Fatalf("stored = %#v", stored)
	}
	if thread.Title != "persist this thread" {
		t.Fatalf("title = %q", thread.Title)
	}

	messages, err := store.ListMessages(ctx, namespace, created.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 2 || messages[0].Content != "persist this thread" {
		t.Fatalf("messages = %#v", messages)
	}

	var schemaExists bool
	if err := store.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.schemata WHERE schema_name = $1
)`, SchemaName).Scan(&schemaExists); err != nil {
		t.Fatalf("lookup chat schema: %v", err)
	}
	if !schemaExists {
		t.Fatal("expected anvil_agents_chat schema")
	}

	var checkpointTable string
	if err := store.pool.QueryRow(ctx, `
SELECT table_name
FROM information_schema.tables
WHERE table_schema = $1 AND table_name = 'checkpoints'`, SchemaName).Scan(&checkpointTable); err != nil {
		t.Fatalf("lookup checkpoints table: %v", err)
	}
	if checkpointTable != "checkpoints" {
		t.Fatalf("checkpoints table = %q", checkpointTable)
	}

	other, err := store.CreateThread(ctx, Thread{
		Namespace: "other-ns",
		Mode:      ModeFleet,
		CreatedBy: "user-2",
	})
	if err != nil {
		t.Fatalf("create fleet thread: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = store.pool.Exec(cleanupCtx, `DELETE FROM anvil_agents_chat.threads WHERE id = $1`, other.ID)
	}()
	sameNS, err := store.ListThreads(ctx, ThreadFilter{Namespace: namespace, ProfileName: profile})
	if err != nil {
		t.Fatalf("list after fleet insert: %v", err)
	}
	if len(sameNS) != 1 || sameNS[0].ID != created.ID {
		t.Fatalf("namespace isolation failed: %#v", sameNS)
	}
}
