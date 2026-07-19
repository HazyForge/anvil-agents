package archive

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestPostgresAgentRunArchiveStoreIntegration(t *testing.T) {
	databaseURL := os.Getenv("ANVIL_AGENTS_TEST_ARCHIVE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ANVIL_AGENTS_TEST_ARCHIVE_DATABASE_URL to run the PostgreSQL archive integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := OpenPostgresAgentRunArchiveStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL archive store: %v", err)
	}
	defer store.Close()

	name := "integration-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	record := AgentRunArchiveRecord{
		Namespace:       "archive-integration",
		Name:            name,
		UID:             name + "-uid",
		ResourceVersion: "1",
		Phase:           "Succeeded",
		ArchivedAt:      time.Now().UTC(),
		Spec:            json.RawMessage(`{"purpose":"manual"}`),
		Status:          json.RawMessage(`{"phase":"Succeeded"}`),
		Output:          "first archive",
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = store.pool.Exec(cleanupCtx, `
DELETE FROM anvilhub_agent_run_archives
WHERE namespace = $1 AND name = $2 AND run_uid = $3`, record.Namespace, record.Name, record.UID)
	}()

	result, err := store.ArchiveAgentRun(ctx, record)
	if err != nil {
		t.Fatalf("archive AgentRun: %v", err)
	}
	if result.Store != AgentRunArchiveStorePostgres || result.Digest == "" {
		t.Fatalf("archive result = %#v", result)
	}

	var phase, output, digest string
	if err := store.pool.QueryRow(ctx, `
SELECT phase, output, digest
FROM anvilhub_agent_run_archives
WHERE namespace = $1 AND name = $2 AND run_uid = $3`, record.Namespace, record.Name, record.UID).Scan(&phase, &output, &digest); err != nil {
		t.Fatalf("read archived AgentRun: %v", err)
	}
	if phase != record.Phase || output != record.Output || digest != result.Digest {
		t.Fatalf("archived row = phase %q output %q digest %q, want record/result", phase, output, digest)
	}

	record.ResourceVersion = "2"
	record.Output = "updated archive"
	record.Status = json.RawMessage(`{"phase":"Succeeded","output":"updated archive"}`)
	updated, err := store.ArchiveAgentRun(ctx, record)
	if err != nil {
		t.Fatalf("upsert AgentRun archive: %v", err)
	}
	var count int
	if err := store.pool.QueryRow(ctx, `
SELECT COUNT(*), MAX(output)
FROM anvilhub_agent_run_archives
WHERE namespace = $1 AND name = $2 AND run_uid = $3`, record.Namespace, record.Name, record.UID).Scan(&count, &output); err != nil {
		t.Fatalf("read upserted AgentRun archive: %v", err)
	}
	if count != 1 || output != record.Output || updated.Digest == result.Digest {
		t.Fatalf("upserted row count=%d output=%q oldDigest=%q newDigest=%q", count, output, result.Digest, updated.Digest)
	}
}
