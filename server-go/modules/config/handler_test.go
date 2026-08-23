package config

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/RakuenSoftware/aimee-module-config/server-go/bus"
	configcontract "github.com/RakuenSoftware/aimee-module-config/server-go/config"
)

func invoke(t *testing.T, handler bus.ModuleHandler, request configcontract.Request) configcontract.Response {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	reply, status := handler(bus.ModuleInvocation{StageID: configcontract.StageConfig}, body)
	if status != bus.ModuleStatusOK {
		t.Fatalf("status = %d", status)
	}
	var response configcontract.Response
	if err := json.Unmarshal(reply, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHandlerOwnsReadMutationAndVersioning(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store)
	set := invoke(t, handler, configcontract.Request{Operation: configcontract.OpSetVersioned,
		Key: "autonomy.max_turns", Value: 321})
	if !set.OK {
		t.Fatalf("set = %+v", set)
	}
	get := invoke(t, handler, configcontract.Request{Operation: configcontract.OpValue,
		Key: "autonomy.max_turns"})
	if !get.OK || get.Value != float64(321) {
		t.Fatalf("get = %+v", get)
	}
}

func TestHandlerRejectsMalformedAndUnknownOperations(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	handler := NewHandler(store)
	if _, status := handler(bus.ModuleInvocation{StageID: configcontract.StageConfig}, []byte("{")); status != bus.ModuleStatusInvalidRequest {
		t.Fatalf("malformed status = %d", status)
	}
	if _, status := handler(bus.ModuleInvocation{StageID: configcontract.StageConfig},
		[]byte("{\"operation\":\"values\"}{}")); status != bus.ModuleStatusInvalidRequest {
		t.Fatalf("trailing payload status = %d", status)
	}
	response := invoke(t, handler, configcontract.Request{Operation: "unknown"})
	if response.OK || response.Code != "invalid_operation" {
		t.Fatalf("unknown response = %+v", response)
	}
}

func TestHandlerOwnsCompleteProfileLifecycle(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	handler := NewHandler(store)
	present := invoke(t, handler, configcontract.Request{Operation: configcontract.OpProfilePresent,
		Value: configcontract.ProfileMutation{Name: "coder"}})
	if !present.OK || present.Present == nil || *present.Present {
		t.Fatalf("initial presence = %+v", present)
	}
	created := invoke(t, handler, configcontract.Request{Operation: configcontract.OpProfileCreate,
		Value: configcontract.ProfileMutation{Name: "coder"}})
	if !created.OK {
		t.Fatalf("create = %+v", created)
	}
	listed := invoke(t, handler, configcontract.Request{Operation: configcontract.OpProfileList})
	if !listed.OK || listed.Profiles == nil || len(*listed.Profiles) != 1 || (*listed.Profiles)[0] != "coder" {
		t.Fatalf("list = %+v", listed)
	}
	deleted := invoke(t, handler, configcontract.Request{Operation: configcontract.OpProfileDelete,
		Value: configcontract.ProfileMutation{Name: "coder"}})
	if !deleted.OK {
		t.Fatalf("delete = %+v", deleted)
	}
}
