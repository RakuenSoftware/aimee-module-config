package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintDefaultValueUsesOwnedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("embedder_model: bekko-a25m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIMEE_CONFIG_PATH", path)
	var output bytes.Buffer
	if err := printDefaultValue("embedder_model", &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "bekko-a25m\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestPrintDefaultValueRejectsSecretsAndNonStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("kb_api_http_port: 8741\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIMEE_CONFIG_PATH", path)
	for _, key := range []string{"kb_api_http_port", "kb_api_bearer_token"} {
		var output bytes.Buffer
		if err := printDefaultValue(key, &output); err == nil ||
			(!strings.Contains(err.Error(), "public string") && !strings.Contains(err.Error(), "not a string")) {
			t.Fatalf("printDefaultValue(%q) error = %v", key, err)
		}
	}
}

func TestPrintDefaultSnapshotUsesSameFilteredStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("provider: codex\nkb_api_bearer_token: forbidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIMEE_CONFIG_PATH", path)
	var output bytes.Buffer
	if err := printDefaultSnapshot(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"provider":"codex"`) {
		t.Fatalf("snapshot missing public value: %s", output.String())
	}
	if strings.Contains(output.String(), "forbidden") || strings.Contains(output.String(), "kb_api_bearer_token") {
		t.Fatalf("snapshot leaked secret: %s", output.String())
	}
}
