package host

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

const maxFrameBytes = 1 << 20

// Trimming applies only to transport copies. Saved traces remain complete.
func frame(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	if len(raw) <= maxFrameBytes {
		return raw
	}
	var tree map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&tree) != nil {
		return nil
	}
	body, ok := tree["body"].(map[string]any)
	if !ok {
		return nil
	}
	for limit := 64 << 10; limit >= 16; limit /= 2 {
		trimmed := trimValue(body, limit).(map[string]any)
		trimmed["truncated"] = true
		tree["body"] = trimmed
		raw, err = json.Marshal(tree)
		if err == nil && len(raw) <= maxFrameBytes {
			return raw
		}
	}
	tree["body"] = map[string]any{"truncated": true}
	raw, _ = json.Marshal(tree)
	return raw
}

func trimValue(value any, limit int) any {
	switch v := value.(type) {
	case string:
		limit = max(256, limit) // Keep UUIDs, timestamps and other short protocol values intact.
		if len(v) <= limit {
			return v
		}
		end := limit
		for end > 0 && !utf8.RuneStart(v[end]) {
			end--
		}
		return v[:end] + "…"
	case []any:
		count := min(len(v), max(1, limit/256))
		out := make([]any, count)
		for i := range out {
			out[i] = trimValue(v[i], limit)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			// Raw model output is arbitrary JSON; its transport copy can be
			// text while the typed trace and event fields retain their shape.
			if key == "rawDecision" {
				raw, _ := json.Marshal(value)
				if len(raw) > limit {
					value = string(raw)
				}
			}
			out[key] = trimValue(value, limit)
		}
		return out
	default:
		return v
	}
}
