package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
)

// RepairAssistantReply atomically replaces a legacy assistant body after the
// API verifies its original native execution. Message identity/order and all
// existing metadata remain intact. A stale reader cannot overwrite a repair.
func repairedAssistant(original Message, content, format string) (Message, error) {
	var metadata map[string]any
	if original.Role != RoleAssistant || original.ID == "" || original.ThreadID == "" || strings.TrimSpace(content) == "" || len(content) > 64*1024 || format != "openclaw.payloads/v1" || json.Unmarshal(original.Metadata, &metadata) != nil || metadata["backend"] != "openClaw" || metadata["replyFormat"] != nil {
		return Message{}, ErrInvalid
	}
	if metadata["turnId"] == nil || metadata["runName"] == nil {
		return Message{}, ErrInvalid
	}
	metadata["replyFormat"] = format
	repaired := original
	repaired.Content = content
	repaired.Metadata, _ = json.Marshal(metadata)
	return repaired, nil
}

func (s *PostgresStore) RepairAssistantReply(ctx context.Context, namespace string, original Message, content, format string) (Message, error) {
	repaired, err := repairedAssistant(original, content, format)
	if err != nil {
		return Message{}, err
	}
	result, err := s.pool.Exec(ctx, `UPDATE anvil_agents_chat.messages AS m SET content=$1, metadata=$2::jsonb
 FROM anvil_agents_chat.threads AS t
 WHERE t.namespace=$3 AND t.id=m.thread_id AND m.thread_id=$4 AND m.id=$5
 AND m.role='assistant' AND m.content=$6 AND m.metadata=$7::jsonb`, repaired.Content, string(repaired.Metadata), namespace, original.ThreadID, original.ID, original.Content, string(original.Metadata))
	if err != nil {
		return Message{}, err
	}
	if result.RowsAffected() != 1 {
		return Message{}, ErrRequestConflict
	}
	return repaired, nil
}

func (s *MemoryStore) RepairAssistantReply(_ context.Context, namespace string, original Message, content, format string) (Message, error) {
	repaired, err := repairedAssistant(original, content, format)
	if err != nil {
		return Message{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.threadKey(namespace, original.ThreadID)
	var expected map[string]any
	_ = json.Unmarshal(original.Metadata, &expected)
	for i, current := range s.messages[key] {
		if current.ID != original.ID {
			continue
		}
		var metadata map[string]any
		_ = json.Unmarshal(current.Metadata, &metadata)
		if current.Role != RoleAssistant || current.Content != original.Content || !reflect.DeepEqual(metadata, expected) {
			return Message{}, ErrRequestConflict
		}
		current.Content = repaired.Content
		current.Metadata = repaired.Metadata
		s.messages[key][i] = current
		return cloneMessages([]Message{current})[0], nil
	}
	return Message{}, ErrRequestConflict
}
