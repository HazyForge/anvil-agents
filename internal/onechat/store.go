package onechat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type State struct {
	Sessions      []Session         `json:"sessions"`
	History       []Message         `json:"history"`
	CloudBindings map[string]string `json:"cloudBindings"`
}

func loadState(dir string) (State, error) {
	s := State{Sessions: []Session{}, History: []Message{}, CloudBindings: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(dir, "onechat.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("invalid onechat state: %w", err)
	}
	if s.CloudBindings == nil {
		s.CloudBindings = map[string]string{}
	}
	return s, nil
}

// writeJSON publishes either the previous complete document or the new one.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".onechat-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func truncate(text string, count int) string {
	runes := []rune(text)
	if len(runes) <= count {
		return text
	}
	return string(runes[:count-1]) + "…"
}

func snapshot(sessions []Session) []Session {
	out := make([]Session, len(sessions))
	for i, s := range sessions {
		messages := s.Transcript
		if len(messages) > 6 {
			messages = messages[len(messages)-6:]
		}
		s.Transcript = append([]Message{}, messages...)
		for j := range s.Transcript {
			s.Transcript[j].Text = truncate(s.Transcript[j].Text, 600)
		}
		out[i] = s
	}
	return out
}
