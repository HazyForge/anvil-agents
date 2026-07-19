package controller

import "testing"

func TestDefaultOptionsDoNotMaterializeArchiveDatabaseURL(t *testing.T) {
	t.Setenv(archiveDatabaseURLEnv, "postgres://operator:secret@example.invalid/archive")

	options := DefaultOptions()
	if options.AgentRunArchiveDatabaseURL != "" {
		t.Fatal("DefaultOptions materialized a Secret-backed database URL")
	}

	applySensitiveEnvironment(options)
	if got, want := options.AgentRunArchiveDatabaseURL, "postgres://operator:secret@example.invalid/archive"; got != want {
		t.Fatalf("archive database URL = %q, want environment value", got)
	}
}

func TestSensitiveEnvironmentDoesNotOverrideProgrammaticArchiveDatabaseURL(t *testing.T) {
	t.Setenv(archiveDatabaseURLEnv, "postgres://environment.invalid/archive")
	options := DefaultOptions()
	options.AgentRunArchiveDatabaseURL = "postgres://programmatic.invalid/archive"

	applySensitiveEnvironment(options)
	if got, want := options.AgentRunArchiveDatabaseURL, "postgres://programmatic.invalid/archive"; got != want {
		t.Fatalf("archive database URL = %q, want explicit value", got)
	}
}
