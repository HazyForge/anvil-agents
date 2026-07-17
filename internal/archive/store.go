package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentRunArchiveStorePostgres retains the historical wire value so existing
// clients can consume archives before and after the standalone takeover.
const AgentRunArchiveStorePostgres = "anvilhub-postgres"

const agentRunArchiveSchema = `
CREATE TABLE IF NOT EXISTS anvilhub_agent_run_archives (
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    run_uid TEXT NOT NULL,
    resource_version TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL,
    backend TEXT NOT NULL DEFAULT '',
    intent TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '',
    source_kind TEXT NOT NULL DEFAULT '',
    source_namespace TEXT NOT NULL DEFAULT '',
    source_name TEXT NOT NULL DEFAULT '',
    schedule_namespace TEXT NOT NULL DEFAULT '',
    schedule_name TEXT NOT NULL DEFAULT '',
    situation_namespace TEXT NOT NULL DEFAULT '',
    situation_name TEXT NOT NULL DEFAULT '',
    runner_node TEXT NOT NULL DEFAULT '',
    pull_request_url TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    archived_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    digest TEXT NOT NULL,
    labels JSONB NOT NULL DEFAULT '{}'::jsonb,
    annotations JSONB NOT NULL DEFAULT '{}'::jsonb,
    spec JSONB NOT NULL,
    status JSONB NOT NULL,
    decision JSONB,
    reports JSONB NOT NULL DEFAULT '[]'::jsonb,
    result JSONB,
    output TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (namespace, name, run_uid)
);
CREATE INDEX IF NOT EXISTS anvilhub_agent_run_archives_completed_idx
    ON anvilhub_agent_run_archives (completed_at DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS anvilhub_agent_run_archives_schedule_idx
    ON anvilhub_agent_run_archives (namespace, schedule_name, completed_at DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS anvilhub_agent_run_archives_phase_idx
    ON anvilhub_agent_run_archives (phase, completed_at DESC NULLS LAST);
`

type AgentRunArchiveStore interface {
	ArchiveAgentRun(ctx context.Context, record AgentRunArchiveRecord) (AgentRunArchiveResult, error)
	Close()
}

type AgentRunArchiveRecord struct {
	Namespace          string
	Name               string
	UID                string
	ResourceVersion    string
	Phase              string
	Backend            string
	Intent             string
	Image              string
	SourceKind         string
	SourceNamespace    string
	SourceName         string
	ScheduleNamespace  string
	ScheduleName       string
	SituationNamespace string
	SituationName      string
	RunnerNode         string
	PullRequestURL     string
	Error              string
	CreatedAt          time.Time
	StartedAt          time.Time
	CompletedAt        time.Time
	ArchivedAt         time.Time
	Digest             string
	Labels             json.RawMessage
	Annotations        json.RawMessage
	Spec               json.RawMessage
	Status             json.RawMessage
	Decision           json.RawMessage
	Reports            json.RawMessage
	Result             json.RawMessage
	Output             string
}

type AgentRunArchiveResult struct {
	Store      string
	ArchivedAt time.Time
	Digest     string
}

type PostgresAgentRunArchiveStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresAgentRunArchiveStore(ctx context.Context, databaseURL string) (*PostgresAgentRunArchiveStore, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse AgentRun archive database URL: %w", err)
	}
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open AgentRun archive database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping AgentRun archive database: %w", err)
	}
	store := &PostgresAgentRunArchiveStore{pool: pool}
	if err := store.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresAgentRunArchiveStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresAgentRunArchiveStore) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, agentRunArchiveSchema); err != nil {
		return fmt.Errorf("migrate AgentRun archive schema: %w", err)
	}
	return nil
}

func (s *PostgresAgentRunArchiveStore) ArchiveAgentRun(ctx context.Context, record AgentRunArchiveRecord) (AgentRunArchiveResult, error) {
	record, err := NormalizeAgentRunArchiveRecord(record)
	if err != nil {
		return AgentRunArchiveResult{}, err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO anvilhub_agent_run_archives (
    namespace, name, run_uid, resource_version, phase, backend, intent, image,
    source_kind, source_namespace, source_name,
    schedule_namespace, schedule_name, situation_namespace, situation_name,
    runner_node, pull_request_url, error,
    created_at, started_at, completed_at, archived_at, digest,
    labels, annotations, spec, status, decision, reports, result, output
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    $9, $10, $11,
    $12, $13, $14, $15,
    $16, $17, $18,
    $19, $20, $21, $22, $23,
    $24, $25, $26, $27, $28, $29, $30, $31
)
ON CONFLICT (namespace, name, run_uid) DO UPDATE SET
    resource_version = EXCLUDED.resource_version,
    phase = EXCLUDED.phase,
    backend = EXCLUDED.backend,
    intent = EXCLUDED.intent,
    image = EXCLUDED.image,
    source_kind = EXCLUDED.source_kind,
    source_namespace = EXCLUDED.source_namespace,
    source_name = EXCLUDED.source_name,
    schedule_namespace = EXCLUDED.schedule_namespace,
    schedule_name = EXCLUDED.schedule_name,
    situation_namespace = EXCLUDED.situation_namespace,
    situation_name = EXCLUDED.situation_name,
    runner_node = EXCLUDED.runner_node,
    pull_request_url = EXCLUDED.pull_request_url,
    error = EXCLUDED.error,
    created_at = EXCLUDED.created_at,
    started_at = EXCLUDED.started_at,
    completed_at = EXCLUDED.completed_at,
    archived_at = EXCLUDED.archived_at,
    updated_at = NOW(),
    digest = EXCLUDED.digest,
    labels = EXCLUDED.labels,
    annotations = EXCLUDED.annotations,
    spec = EXCLUDED.spec,
    status = EXCLUDED.status,
    decision = EXCLUDED.decision,
    reports = EXCLUDED.reports,
    result = EXCLUDED.result,
    output = EXCLUDED.output`,
		record.Namespace,
		record.Name,
		record.UID,
		record.ResourceVersion,
		record.Phase,
		record.Backend,
		record.Intent,
		record.Image,
		record.SourceKind,
		record.SourceNamespace,
		record.SourceName,
		record.ScheduleNamespace,
		record.ScheduleName,
		record.SituationNamespace,
		record.SituationName,
		record.RunnerNode,
		record.PullRequestURL,
		record.Error,
		timestamptz(record.CreatedAt),
		timestamptz(record.StartedAt),
		timestamptz(record.CompletedAt),
		timestamptz(record.ArchivedAt),
		record.Digest,
		[]byte(record.Labels),
		[]byte(record.Annotations),
		[]byte(record.Spec),
		[]byte(record.Status),
		nullableJSON(record.Decision),
		[]byte(record.Reports),
		nullableJSON(record.Result),
		record.Output,
	)
	if err != nil {
		return AgentRunArchiveResult{}, fmt.Errorf("archive AgentRun %s/%s: %w", record.Namespace, record.Name, err)
	}
	return AgentRunArchiveResult{
		Store:      AgentRunArchiveStorePostgres,
		ArchivedAt: record.ArchivedAt,
		Digest:     record.Digest,
	}, nil
}

func NewAgentRunArchiveRecord(run *controlv1alpha1.AgentRun, archivedAt time.Time) (AgentRunArchiveRecord, error) {
	if run == nil {
		return AgentRunArchiveRecord{}, fmt.Errorf("AgentRun is required")
	}
	if archivedAt.IsZero() {
		archivedAt = time.Now().UTC()
	}
	spec, err := json.Marshal(run.Spec)
	if err != nil {
		return AgentRunArchiveRecord{}, fmt.Errorf("marshal AgentRun spec: %w", err)
	}
	status, err := json.Marshal(run.Status)
	if err != nil {
		return AgentRunArchiveRecord{}, fmt.Errorf("marshal AgentRun status: %w", err)
	}
	labels, err := json.Marshal(run.Labels)
	if err != nil {
		return AgentRunArchiveRecord{}, fmt.Errorf("marshal AgentRun labels: %w", err)
	}
	annotations, err := json.Marshal(run.Annotations)
	if err != nil {
		return AgentRunArchiveRecord{}, fmt.Errorf("marshal AgentRun annotations: %w", err)
	}
	decision, err := json.Marshal(run.Status.Decision)
	if err != nil {
		return AgentRunArchiveRecord{}, fmt.Errorf("marshal AgentRun decision: %w", err)
	}
	if run.Status.Decision == nil {
		decision = nil
	}
	reports, err := json.Marshal(run.Status.Reports)
	if err != nil {
		return AgentRunArchiveRecord{}, fmt.Errorf("marshal AgentRun reports: %w", err)
	}
	result := run.Status.Result.Raw
	if len(result) == 0 {
		result = nil
	}
	record := AgentRunArchiveRecord{
		Namespace:       strings.TrimSpace(run.Namespace),
		Name:            strings.TrimSpace(run.Name),
		UID:             string(run.UID),
		ResourceVersion: strings.TrimSpace(run.ResourceVersion),
		Phase:           string(run.Status.Phase),
		Backend:         strings.TrimSpace(run.Status.Backend),
		Intent:          strings.TrimSpace(run.Status.Intent),
		Image:           strings.TrimSpace(run.Status.Image),
		SourceKind:      strings.TrimSpace(run.Spec.SourceRef.Kind),
		SourceNamespace: strings.TrimSpace(run.Spec.SourceRef.Namespace),
		SourceName:      strings.TrimSpace(run.Spec.SourceRef.Name),
		RunnerNode:      strings.TrimSpace(run.Status.RunnerNode),
		PullRequestURL:  strings.TrimSpace(run.Status.PullRequestURL),
		Error:           strings.TrimSpace(run.Status.Error),
		CreatedAt:       metav1Time(run.CreationTimestamp),
		StartedAt:       metav1PtrTime(run.Status.StartedAt),
		CompletedAt:     metav1PtrTime(run.Status.CompletedAt),
		ArchivedAt:      archivedAt.UTC(),
		Labels:          labels,
		Annotations:     annotations,
		Spec:            spec,
		Status:          status,
		Decision:        decision,
		Reports:         reports,
		Result:          result,
		Output:          strings.TrimSpace(run.Status.Output),
	}
	if run.Spec.ScheduleRef != nil {
		record.ScheduleNamespace = strings.TrimSpace(run.Spec.ScheduleRef.Namespace)
		record.ScheduleName = strings.TrimSpace(run.Spec.ScheduleRef.Name)
	}
	if run.Spec.SituationRef != nil {
		record.SituationNamespace = strings.TrimSpace(run.Spec.SituationRef.Namespace)
		record.SituationName = strings.TrimSpace(run.Spec.SituationRef.Name)
	}
	normalized, err := NormalizeAgentRunArchiveRecord(record)
	if err != nil {
		return AgentRunArchiveRecord{}, err
	}
	return normalized, nil
}

func NormalizeAgentRunArchiveRecord(record AgentRunArchiveRecord) (AgentRunArchiveRecord, error) {
	record.Namespace = strings.TrimSpace(record.Namespace)
	record.Name = strings.TrimSpace(record.Name)
	record.UID = strings.TrimSpace(record.UID)
	record.ResourceVersion = strings.TrimSpace(record.ResourceVersion)
	record.Phase = strings.TrimSpace(record.Phase)
	record.Backend = strings.TrimSpace(record.Backend)
	record.Intent = strings.TrimSpace(record.Intent)
	record.Image = strings.TrimSpace(record.Image)
	record.SourceKind = strings.TrimSpace(record.SourceKind)
	record.SourceNamespace = strings.TrimSpace(record.SourceNamespace)
	record.SourceName = strings.TrimSpace(record.SourceName)
	record.ScheduleNamespace = strings.TrimSpace(record.ScheduleNamespace)
	record.ScheduleName = strings.TrimSpace(record.ScheduleName)
	record.SituationNamespace = strings.TrimSpace(record.SituationNamespace)
	record.SituationName = strings.TrimSpace(record.SituationName)
	record.RunnerNode = strings.TrimSpace(record.RunnerNode)
	record.PullRequestURL = strings.TrimSpace(record.PullRequestURL)
	record.Error = strings.TrimSpace(record.Error)
	record.Output = strings.TrimSpace(record.Output)
	if record.Namespace == "" {
		return AgentRunArchiveRecord{}, fmt.Errorf("AgentRun namespace is required")
	}
	if record.Name == "" {
		return AgentRunArchiveRecord{}, fmt.Errorf("AgentRun name is required")
	}
	if record.UID == "" {
		return AgentRunArchiveRecord{}, fmt.Errorf("AgentRun UID is required")
	}
	if record.Phase == "" {
		return AgentRunArchiveRecord{}, fmt.Errorf("AgentRun phase is required")
	}
	if record.ArchivedAt.IsZero() {
		record.ArchivedAt = time.Now().UTC()
	}
	if len(record.Labels) == 0 {
		record.Labels = []byte(`{}`)
	}
	if len(record.Annotations) == 0 {
		record.Annotations = []byte(`{}`)
	}
	if len(record.Spec) == 0 {
		return AgentRunArchiveRecord{}, fmt.Errorf("AgentRun spec archive is required")
	}
	if len(record.Status) == 0 {
		return AgentRunArchiveRecord{}, fmt.Errorf("AgentRun status archive is required")
	}
	if len(record.Reports) == 0 {
		record.Reports = []byte(`[]`)
	}
	digest, err := agentRunArchiveDigest(record)
	if err != nil {
		return AgentRunArchiveRecord{}, err
	}
	record.Digest = digest
	return record, nil
}

func agentRunArchiveDigest(record AgentRunArchiveRecord) (string, error) {
	body, err := json.Marshal(struct {
		Namespace       string          `json:"namespace"`
		Name            string          `json:"name"`
		UID             string          `json:"uid"`
		ResourceVersion string          `json:"resourceVersion"`
		Phase           string          `json:"phase"`
		Spec            json.RawMessage `json:"spec"`
		Status          json.RawMessage `json:"status"`
	}{
		Namespace:       record.Namespace,
		Name:            record.Name,
		UID:             record.UID,
		ResourceVersion: record.ResourceVersion,
		Phase:           record.Phase,
		Spec:            record.Spec,
		Status:          record.Status,
	})
	if err != nil {
		return "", fmt.Errorf("marshal AgentRun archive digest body: %w", err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}

func metav1PtrTime(value *metav1.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.Time
}

func metav1Time(value metav1.Time) time.Time {
	return value.Time
}
