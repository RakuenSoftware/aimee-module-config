package config

import (
	"encoding/json"
	"testing"
)

func TestMetadataIsUniquePublicAndValid(t *testing.T) {
	var metadata struct {
		Version int             `json:"version"`
		Fields  []fieldMetadata `json:"fields"`
	}
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Version != 1 || len(metadata.Fields) == 0 {
		t.Fatalf("invalid metadata header: version=%d fields=%d", metadata.Version, len(metadata.Fields))
	}
	seen := map[string]bool{}
	for _, field := range metadata.Fields {
		if seen[field.Key] {
			t.Fatalf("duplicate metadata key %q", field.Key)
		}
		seen[field.Key] = true
		if !publicKey(field.Key) {
			t.Fatalf("credential key %q leaked into config metadata", field.Key)
		}
		switch field.Type {
		case "string", "string (off|safe|aggressive)", "bool", "int", "float":
		default:
			t.Fatalf("unsupported metadata type %q for %q", field.Type, field.Key)
		}
	}
}

func TestMetadataBacksLegacyScalarMutations(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/aimee.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"guardrail_mode":                     "warn",
		"embedder_command":                   "embed",
		"cross_verify":                       true,
		"max_iterations":                     json.Number("42"),
		"guardrails_semantic_warn_threshold": json.Number("0.5"),
	} {
		if err := store.Set(key, value); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
	}
}
