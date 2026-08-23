package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/RakuenSoftware/aimee-module-config/server-go/bus"
	configcontract "github.com/RakuenSoftware/aimee-module-config/server-go/config"
)

func TestHandlerConformsToOwnedEventContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "src", "modules", "config",
		"eventcontract", "operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		EventKind uint32 `json:"event_kind"`
		StageID   uint32 `json:"stage_id"`
		Vectors   []struct {
			Name         string `json:"name"`
			RequestJSON  string `json:"request_json"`
			ResponseJSON string `json:"response_json"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.EventKind != configcontract.EventConfig || document.StageID != configcontract.StageConfig ||
		len(document.Vectors) == 0 {
		t.Fatalf("contract identity/vectors = %d/%d/%d", document.EventKind, document.StageID,
			len(document.Vectors))
	}
	for _, vector := range document.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			store, err := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			reply, status := NewHandler(store)(bus.ModuleInvocation{StageID: document.StageID},
				[]byte(vector.RequestJSON))
			if status != bus.ModuleStatusOK || string(reply) != vector.ResponseJSON {
				t.Fatalf("response = %s (status %d), want %s", reply, status, vector.ResponseJSON)
			}
		})
	}
}
