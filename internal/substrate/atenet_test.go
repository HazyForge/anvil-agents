package substrate

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActorAuthority(t *testing.T) {
	t.Parallel()
	got := ActorAuthority("chat-thread-1", "anvilhub")
	want := "chat-thread-1.anvilhub.actors.resources.substrate.ate.dev"
	if got != want {
		t.Fatalf("authority = %q, want %q", got, want)
	}
	if ActorAuthority("", "anvilhub") != "" || ActorAuthority("chat-1", "") != "" {
		t.Fatal("missing name or atespace must yield empty authority")
	}
}

func TestAtenetGenerateACPStream(t *testing.T) {
	t.Parallel()

	var sawHost, sawAuth string
	var sawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sawHost = request.Host
		sawAuth = request.Header.Get("Authorization")
		sawBody, _ = io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(sawBody))
		ServeACPEcho(writer, request)
	}))
	t.Cleanup(server.Close)

	client, err := NewAtenetClientHTTP(AtenetConfig{Endpoint: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Generate(context.Background(), GenerateRequest{
		ActorName: "chat-thread-1",
		Atespace:  "anvilhub",
		SessionID: "thread-1",
		Prompt:    "hello from frozen prompt",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result.Text != "hello from frozen prompt" || result.StopReason != "end_turn" {
		t.Fatalf("result = %+v", result)
	}
	if sawHost != ActorAuthority("chat-thread-1", "anvilhub") {
		t.Fatalf("host = %q, want actor DNS name", sawHost)
	}
	if sawAuth != "" {
		t.Fatal("generate without token must not send Authorization")
	}
	if !bytes.Contains(sawBody, []byte(`"method":"session/prompt"`)) || !bytes.Contains(sawBody, []byte("hello from frozen prompt")) {
		t.Fatalf("ACP body = %s", sawBody)
	}
}

func TestAtenetGeneratePlainTextAndJSONText(t *testing.T) {
	t.Parallel()

	plain := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte("plain actor reply"))
	}))
	t.Cleanup(plain.Close)
	plainClient, err := NewAtenetClientHTTP(AtenetConfig{Endpoint: plain.URL}, plain.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := plainClient.Generate(context.Background(), GenerateRequest{ActorName: "a", Atespace: "ns", Prompt: "p"})
	if err != nil || got.Text != "plain actor reply" {
		t.Fatalf("plain = %+v err=%v", got, err)
	}

	wrapped := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"text":"json text reply"}`))
	}))
	t.Cleanup(wrapped.Close)
	jsonClient, err := NewAtenetClientHTTP(AtenetConfig{Endpoint: wrapped.URL}, wrapped.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err = jsonClient.Generate(context.Background(), GenerateRequest{ActorName: "a", Atespace: "ns", Prompt: "p"})
	if err != nil || got.Text != "json text reply" {
		t.Fatalf("json text = %+v err=%v", got, err)
	}
}

func TestAtenetGenerateTransientAndPermanent(t *testing.T) {
	t.Parallel()

	parked := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "actor unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(parked.Close)
	client, err := NewAtenetClientHTTP(AtenetConfig{Endpoint: parked.URL}, parked.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), GenerateRequest{ActorName: "a", Atespace: "ns", Prompt: "p"})
	if !errors.Is(err, ErrGenerateTransient) {
		t.Fatalf("503 = %v, want ErrGenerateTransient", err)
	}

	missing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "no such actor", http.StatusNotFound)
	}))
	t.Cleanup(missing.Close)
	client, err = NewAtenetClientHTTP(AtenetConfig{Endpoint: missing.URL}, missing.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), GenerateRequest{ActorName: "a", Atespace: "ns", Prompt: "p"})
	if err == nil || errors.Is(err, ErrGenerateTransient) || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("404 = %v, want permanent HTTP 404", err)
	}
}

func TestAtenetGenerateEmptyReplyFails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	client, err := NewAtenetClientHTTP(AtenetConfig{Endpoint: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), GenerateRequest{ActorName: "a", Atespace: "ns", Prompt: "p"})
	if err == nil || errors.Is(err, ErrGenerateTransient) {
		t.Fatalf("empty 200 = %v, want permanent empty reply", err)
	}
}

func TestAtenetGenerateNeverPrintsToken(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "token")
	secret := "super-secret-atenet-token"
	if err := os.WriteFile(tokenPath, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("authorization = %q", got)
		}
		http.Error(writer, "nope "+secret, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	client, err := NewAtenetClientHTTP(AtenetConfig{Endpoint: server.URL, TokenFile: tokenPath}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), GenerateRequest{ActorName: "a", Atespace: "ns", Prompt: "p"})
	if err == nil {
		t.Fatal("expected 401")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token bytes: %v", err)
	}
}

func TestAtenetGenerateRequiresPrompt(t *testing.T) {
	t.Parallel()
	client, err := NewAtenetClientHTTP(AtenetConfig{Endpoint: "http://127.0.0.1:9"}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), GenerateRequest{ActorName: "a", Atespace: "ns"})
	if err == nil || !strings.Contains(err.Error(), "prompt is empty") {
		t.Fatalf("empty prompt = %v", err)
	}
}
