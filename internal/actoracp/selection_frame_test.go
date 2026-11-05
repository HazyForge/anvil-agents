package actoracp

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// A peer can reply before the request's write acknowledgement is delivered to
// call(). Keep that acknowledgement pending until a subsequent catalog update
// has been observed. This makes the shared-cache overwrite deterministic.
type delayedSelectionWriteAck struct {
	net.Conn
	release chan struct{}
	once    sync.Once
}

func (w *delayedSelectionWriteAck) open()        { w.once.Do(func() { close(w.release) }) }
func (w *delayedSelectionWriteAck) Close() error { w.open(); return w.Conn.Close() }
func (w *delayedSelectionWriteAck) Write(data []byte) (int, error) {
	var request message
	_ = json.Unmarshal(bytes.TrimSpace(data), &request)
	n, err := w.Conn.Write(data)
	if err == nil && request.Method == "session/set_config_option" {
		<-w.release
	}
	return n, err
}

func TestSelectionAcknowledgementUsesCorrelatedResponse(t *testing.T) {
	for _, kind := range []string{"wrong-model", "wrong-mode", "missing-options", "malformed-options", "rpc-error", "valid-ack"} {
		t.Run(kind, func(t *testing.T) {
			var transport *delayedSelectionWriteAck
			adapter, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: time.Second}, func(p *inertPeer, r message, id string, _ int) {
				p.update(id, "agent_message_chunk", "INERT_PROMPT_REACHED")
				p.result(r.ID, map[string]any{"stopReason": "end_turn"})
			}, func(conn net.Conn) io.ReadWriteCloser {
				transport = &delayedSelectionWriteAck{Conn: conn, release: make(chan struct{})}
				return transport
			})
			initialModel := "foreign/bootstrap"
			if kind == "wrong-mode" {
				initialModel = approvedModel
			}
			peer.mu.Lock()
			peer.onNew = func(p *inertPeer, rpcID json.RawMessage, id string) {
				p.result(rpcID, map[string]any{"sessionId": id, "configOptions": selectionOptions(initialModel, "plan", true)})
			}
			peer.mu.Unlock()
			go func() {
				select {
				case r := <-peer.responses:
					if r.Method != "session/set_config_option" {
						t.Errorf("fixture expected set_config_option, got %s", r.Method)
						transport.open()
						return
					}
					switch kind {
					case "wrong-model":
						peer.result(r.ID, map[string]any{"configOptions": selectionOptions("foreign/bootstrap", "plan", true)})
					case "wrong-mode":
						peer.result(r.ID, map[string]any{"configOptions": selectionOptions(approvedModel, "plan", true)})
					case "missing-options":
						peer.result(r.ID, map[string]any{})
					case "malformed-options":
						peer.result(r.ID, map[string]any{"configOptions": map[string]string{"invalid": "not-an-array"}})
					case "rpc-error":
						peer.send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32000, "message": "inert explicit rejection"}})
					case "valid-ack":
						peer.result(r.ID, map[string]any{"configOptions": selectionOptions(approvedModel, "build", true)})
					}
					// Notification is separate evidence; it must not repair an invalid RPC ack.
					selectionUpdate(peer, "native-1", approvedModel, "build", true)
					until := time.Now().Add(time.Second)
					for adapter.client.ready() && !adapter.client.catalog("native-1").selected(approvedModel, "build") && time.Now().Before(until) {
						time.Sleep(time.Millisecond)
					}
					transport.open()
				case <-peer.closed:
					transport.open()
				}
			}()
			_, err := generate(t, server, "ack-review-"+kind, "inert-only")
			peer.mu.Lock()
			count := len(peer.prompts)
			peer.mu.Unlock()
			if kind == "valid-ack" {
				if err != nil || count != 1 {
					t.Fatalf("valid ack control failed: prompts=%d err=%v", count, err)
				}
			} else if err == nil || count != 0 {
				t.Errorf("invalid %s ack was overwritten by later update: prompts=%d err=%v", kind, count, err)
			}
		})
	}
}

func TestActiveSelectionDriftFailsTurn(t *testing.T) {
	for _, kind := range []string{"model", "mode", "model-restored", "mode-restored", "missing-options", "malformed-options", "partial-model", "valid-update"} {
		t.Run(kind, func(t *testing.T) {
			adapter, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: time.Second}, func(p *inertPeer, r message, id string, _ int) {
				model, mode := approvedModel, "build"
				if kind == "partial-model" {
					p.update(id, "agent_message_chunk", "INERT_VALID_PARTIAL_BEFORE_DRIFT")
				}
				if kind == "model" || kind == "model-restored" || kind == "partial-model" {
					model = "foreign/bootstrap"
				} else if kind != "valid-update" {
					mode = "plan"
				}
				// Each ordered frame is evidence even if a subsequent frame restores it.
				if kind == "missing-options" || kind == "malformed-options" {
					update := map[string]any{"sessionUpdate": "config_option_update"}
					if kind == "malformed-options" {
						update["configOptions"] = map[string]string{"invalid": "not-an-array"}
					}
					p.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": id, "update": update}})
				} else {
					selectionUpdate(p, id, model, mode, true)
				}
				if kind == "model-restored" || kind == "mode-restored" {
					selectionUpdate(p, id, approvedModel, "build", true)
				}
				p.update(id, "agent_message_chunk", "INERT_REPLY_AFTER_SELECTION_DRIFT")
				p.result(r.ID, map[string]any{"stopReason": "end_turn"})
			})
			peer.mu.Lock()
			peer.onNew = func(p *inertPeer, rpcID json.RawMessage, id string) {
				p.result(rpcID, map[string]any{"sessionId": id, "configOptions": selectionOptions(approvedModel, "build", true)})
			}
			peer.mu.Unlock()
			result, err := generate(t, server, "active-drift-"+kind, "inert-only")
			selected := adapter.client.catalog("native-1").selected(approvedModel, "build")
			if kind == "valid-update" {
				if err != nil || result.Text != "INERT_REPLY_AFTER_SELECTION_DRIFT" {
					t.Errorf("valid update control failed: reply=%q err=%v", result.Text, err)
				}
			} else if err == nil || result.Text != "" || adapter.client.ready() {
				t.Errorf("observed %s drift did not fail and close: selected=%v reply=%q err=%v ready=%v", kind, selected, result.Text, err, adapter.client.ready())
			}
		})
	}
}
