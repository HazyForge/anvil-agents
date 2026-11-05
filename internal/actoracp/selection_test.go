package actoracp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

const approvedModel = "openai/approved-synthetic-model"

func selectionOptions(model, mode string, complete bool) []any {
	models := []any{map[string]any{"value": "foreign/bootstrap"}}
	modes := []any{map[string]any{"value": "plan"}}
	if complete {
		models = append(models, map[string]any{"value": approvedModel})
		modes = append(modes, map[string]any{"value": "build"})
	}
	return []any{
		map[string]any{"id": "model", "type": "select", "currentValue": model, "options": []any{map[string]any{"group": "Provider", "options": models}}},
		map[string]any{"id": "mode", "type": "select", "currentValue": mode, "options": modes},
	}
}

func selectionUpdate(p *inertPeer, id, model, mode string, complete bool) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": id, "update": map[string]any{"sessionUpdate": "config_option_update", "configOptions": selectionOptions(model, mode, complete)}}})
}

func TestSelectionWaitsForIdleCatalogThenPinsExactModelAndMode(t *testing.T) {
	ready := make(chan string, 1)
	release := make(chan struct{})
	var choiceMu sync.Mutex
	model, mode := "foreign/bootstrap", "plan"
	_, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: 500 * time.Millisecond}, func(p *inertPeer, request message, id string, _ int) {
		choiceMu.Lock()
		valid := model == approvedModel && mode == "build"
		choiceMu.Unlock()
		if !valid {
			t.Error("prompt reached wrong native selection")
		}
		p.update(id, "agent_message_chunk", "APPROVED_REPLY")
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, rpcID json.RawMessage, id string) {
		p.result(rpcID, map[string]any{"sessionId": id, "configOptions": selectionOptions("foreign/bootstrap", "plan", false)})
		ready <- id
		<-release
		selectionUpdate(p, id, "foreign/bootstrap", "plan", true)
	}
	peer.mu.Unlock()
	go func() {
		for {
			select {
			case request := <-peer.responses:
				if request.Method != "session/set_config_option" {
					t.Errorf("unexpected request %s", request.Method)
					return
				}
				var params struct {
					ID    string `json:"configId"`
					Value string `json:"value"`
				}
				_ = json.Unmarshal(request.Params, &params)
				choiceMu.Lock()
				if params.ID == "model" {
					model = params.Value
				} else if params.ID == "mode" {
					mode = params.Value
				} else {
					t.Error("unapproved selector")
				}
				options := selectionOptions(model, mode, true)
				choiceMu.Unlock()
				peer.result(request.ID, map[string]any{"configOptions": options})
			case <-peer.closed:
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() {
		result, err := generate(t, server, "thread-delay", "synthetic")
		if err == nil && result.Text != "APPROVED_REPLY" {
			t.Errorf("reply %q", result.Text)
		}
		done <- err
	}()
	<-ready
	peer.mu.Lock()
	before := len(peer.prompts)
	peer.mu.Unlock()
	if before != 0 {
		t.Fatal("prompt before catalog readiness")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.prompts) != 1 || peer.newCount != 1 {
		t.Fatalf("native calls: prompts=%d sessions=%d", len(peer.prompts), peer.newCount)
	}
}

func TestSelectionRejectsMismatchedAcknowledgementWithoutPrompt(t *testing.T) {
	_, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: 200 * time.Millisecond}, func(*inertPeer, message, string, int) { t.Error("unapproved prompt") })
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, rpcID json.RawMessage, id string) {
		p.result(rpcID, map[string]any{"sessionId": id, "configOptions": selectionOptions("foreign/bootstrap", "plan", true)})
	}
	peer.mu.Unlock()
	go func() {
		select {
		case request := <-peer.responses:
			peer.result(request.ID, map[string]any{"configOptions": selectionOptions("foreign/bootstrap", "plan", true)})
		case <-peer.closed:
		}
	}()
	if _, err := generate(t, server, "thread-mismatch", "synthetic"); err == nil {
		t.Fatal("mismatched acknowledgement succeeded")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.prompts) != 0 || peer.newCount != 1 {
		t.Fatal("prompt or session replay after mismatch")
	}
}

func TestSelectionTimeoutAndQueuedCancellationNeverPrompt(t *testing.T) {
	started := make(chan string, 2)
	adapter, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: 150 * time.Millisecond}, func(*inertPeer, message, string, int) { t.Error("prompt during catalog wait") })
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, rpcID json.RawMessage, id string) {
		p.result(rpcID, map[string]any{"sessionId": id, "configOptions": selectionOptions("foreign/bootstrap", "plan", false)})
		started <- id
	}
	peer.mu.Unlock()
	first := make(chan error, 1)
	go func() { _, err := generate(t, server, "waiting-first", "synthetic"); first <- err }()
	<-started
	second := make(chan struct{})
	go func() { _, _ = generate(t, server, "waiting-second", "synthetic"); close(second) }()
	// Wait for the HTTP request to own a busy logical session, then cancel it
	// while it is queued behind the first native setup.
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		adapter.mu.Lock()
		current := adapter.sessions["waiting-second"]
		busy := current != nil && current.busy
		adapter.mu.Unlock()
		if busy {
			break
		}
		time.Sleep(time.Millisecond)
	}
	response := post(t, server, nil, "session/cancel", map[string]string{"sessionId": "waiting-second"})
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	select {
	case <-second:
	case <-time.After(80 * time.Millisecond):
		t.Fatal("queued cancellation did not leave setup queue")
	}
	if err := <-first; err == nil {
		t.Fatal("missing model catalog succeeded")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.prompts) != 0 || peer.newCount != 1 {
		t.Fatal("prompt or second setup after cancellation/timeout")
	}
}

func TestSelectionCancellationDuringReadinessReturnsWithoutPrompt(t *testing.T) {
	started := make(chan struct{}, 1)
	adapter, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: time.Second}, func(*inertPeer, message, string, int) { t.Error("cancelled readiness dispatched") })
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, rpcID json.RawMessage, id string) {
		p.result(rpcID, map[string]any{"sessionId": id, "configOptions": selectionOptions("foreign/bootstrap", "plan", false)})
		started <- struct{}{}
	}
	peer.mu.Unlock()
	done := make(chan struct{})
	go func() {
		_ = post(t, server, 1, "session/prompt", promptParams("cancel-readiness", "synthetic")).Body.Close()
		close(done)
	}()
	<-started
	response := post(t, server, nil, "session/cancel", map[string]any{"sessionId": "cancel-readiness"})
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("readiness cancellation waited for timeout")
	}
	adapter.mu.Lock()
	_, retained := adapter.sessions["cancel-readiness"]
	adapter.mu.Unlock()
	if retained {
		t.Fatal("cancelled setup retained logical slot")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.prompts) != 0 {
		t.Fatal("prompt after cancellation")
	}
}

func TestSelectionRevalidatesIdleChangesAndRejectsHTTPSelectionAuthority(t *testing.T) {
	adapter, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: 100 * time.Millisecond}, func(p *inertPeer, request message, id string, _ int) {
		p.update(id, "agent_message_chunk", "FIXED_REPLY")
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, rpcID json.RawMessage, id string) {
		p.result(rpcID, map[string]any{"sessionId": id, "configOptions": selectionOptions(approvedModel, "build", true)})
	}
	peer.mu.Unlock()
	response := post(t, server, 1, "session/prompt", map[string]any{"sessionId": "fixed-thread", "model": "foreign/evil", "mode": "plan", "prompt": []any{map[string]string{"type": "text", "text": "synthetic"}}})
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(raw) == "" {
		t.Fatal("missing fixed reply")
	}
	selectionUpdate(peer, "native-1", "foreign/bootstrap", "plan", false)
	until := time.Now().Add(time.Second)
	for adapter.client.catalog("native-1").options["model"].current != "foreign/bootstrap" && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if _, err := generate(t, server, "fixed-thread", "second"); err == nil {
		t.Fatal("revoked catalog reused cached proof")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.prompts) != 1 {
		t.Fatal("new prompt after idle metadata changed")
	}
}

func TestSelectionOperatorValidationAndMalformedCatalog(t *testing.T) {
	for _, cfg := range []Config{
		{Workdir: "/tmp", ExpectedModel: approvedModel},
		{Workdir: "/tmp", ExpectedMode: "build"},
		{Workdir: "/tmp", ExpectedModel: "openai", ExpectedMode: "build"},
		{Workdir: "/tmp", ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: 31 * time.Second},
	} {
		// Validation runs before initialize, so no transport is needed for the
		// bad operator values beyond the non-nil identity.
		if _, err := NewServer(context.Background(), &Client{}, cfg); err == nil {
			t.Fatal("invalid operator selection accepted")
		}
	}
	for _, raw := range []string{
		`[{"id":"model","type":"boolean","currentValue":true}]`,
		`[{"id":"model","type":"select","currentValue":"x","options":[]}]`,
		`[{"id":"model","type":"select","currentValue":"x","options":[{"value":"x"}]},{"id":"model","type":"select","currentValue":"x","options":[{"value":"x"}]}]`,
	} {
		if parseCatalog(json.RawMessage(raw)).err == nil {
			t.Fatal("malformed catalog accepted")
		}
	}
}

func waitSelectionTest(t *testing.T, fn func() bool) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("inert synchronization timed out")
}

func TestSelectionWriterRechecksRevocationAfterRequestWasQueued(t *testing.T) {
	adapter, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: time.Second}, func(p *inertPeer, r message, id string, _ int) {
		p.update(id, "agent_message_chunk", "VALID_REPLY")
		p.result(r.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, id json.RawMessage, session string) {
		p.result(id, map[string]any{"sessionId": session, "configOptions": selectionOptions(approvedModel, "build", true)})
	}
	peer.mu.Unlock()
	if _, err := generate(t, server, "queued-thread", "prime"); err != nil {
		t.Fatal(err)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = adapter.client.sendChecked(context.Background(), map[string]any{"jsonrpc": "2.0", "method": "inert-not-sent"}, func() error { close(blocked); <-release; return ErrSelection })
	}()
	<-blocked
	done := make(chan error, 1)
	go func() { _, err := generate(t, server, "queued-thread", "must-not-dispatch"); done <- err }()
	waitSelectionTest(t, func() bool { return len(adapter.client.writes) > 0 })
	selectionUpdate(peer, "native-1", "foreign/bootstrap", "plan", true)
	waitSelectionTest(t, func() bool { return adapter.client.catalog("native-1").options["model"].current == "foreign/bootstrap" })
	close(release)
	if err := <-done; err == nil {
		t.Fatal("revoked queued prompt succeeded")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.prompts) != 1 {
		t.Fatal("stale selection crossed native write boundary")
	}
}

func TestSelectionSharedWriterDoesNotDeadlockAnotherSessionCancellation(t *testing.T) {
	cancelledA := make(chan struct{})
	adapter, peer, server := fixture(t, Config{ExpectedModel: approvedModel, ExpectedMode: "build", CatalogTimeout: time.Second}, func(p *inertPeer, r message, id string, _ int) {
		if id == "native-1" {
			p.update(id, "agent_message_chunk", "A_PENDING")
			select {
			case <-cancelledA:
				p.result(r.ID, map[string]any{"stopReason": "cancelled"})
			case <-p.closed:
			}
			return
		}
		p.update(id, "agent_message_chunk", "B_VALID_REPLY")
		p.result(r.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, id json.RawMessage, session string) {
		p.result(id, map[string]any{"sessionId": session, "configOptions": selectionOptions(approvedModel, "build", true)})
	}
	peer.onCancel = func(_ *inertPeer, id string) {
		if id == "native-1" {
			close(cancelledA)
		}
	}
	peer.mu.Unlock()
	responseA := post(t, server, 1, "session/prompt", promptParams("A", "keep-running"))
	defer responseA.Body.Close()
	if _, err := generate(t, server, "B", "prime-b"); err != nil {
		t.Fatal(err)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = adapter.client.sendChecked(context.Background(), map[string]any{"jsonrpc": "2.0", "method": "inert-not-sent"}, func() error { close(blocked); <-release; return ErrSelection })
	}()
	<-blocked
	doneB := make(chan error, 1)
	go func() { _, err := generate(t, server, "B", "second-b"); doneB <- err }()
	waitSelectionTest(t, func() bool { return len(adapter.client.writes) > 0 })
	doneCancel := make(chan struct{})
	go func() {
		r := post(t, server, nil, "session/cancel", map[string]string{"sessionId": "A"})
		r.Body.Close()
		close(doneCancel)
	}()
	waitSelectionTest(t, func() bool { return len(adapter.client.writes) >= 2 })
	close(release)
	select {
	case err := <-doneB:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("checked B dispatch waited on A cancellation mutex")
	}
	select {
	case <-doneCancel:
	case <-time.After(400 * time.Millisecond):
		t.Fatal("A cancellation remained blocked")
	}
	_, _ = io.ReadAll(responseA.Body)
}
