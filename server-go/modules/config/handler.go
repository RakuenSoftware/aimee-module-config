package config

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/RakuenSoftware/aimee-module-config/server-go/bus"
	configcontract "github.com/RakuenSoftware/aimee-module-config/server-go/config"
)

// NewHandler serves the complete config store contract. The Store owns all
// parsing, validation, versioning, and persistence; the handler only translates
// the language-neutral event payload.
func NewHandler(store *Store) bus.ModuleHandler {
	return func(invocation bus.ModuleInvocation, body []byte) ([]byte, bus.ModuleStatus) {
		if invocation.StageID != configcontract.StageConfig || store == nil {
			return nil, bus.ModuleStatusInvalidRequest
		}
		if invocation.Cancelled() {
			return nil, bus.ModuleStatusCancelled
		}
		var request configcontract.Request
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Operation == "" {
			return nil, bus.ModuleStatusInvalidRequest
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, bus.ModuleStatusInvalidRequest
		}
		response := handleRequest(store, request)
		encoded, err := json.Marshal(response)
		if err != nil || uint32(len(encoded)) > bus.ModuleMessageMaxBody {
			return nil, bus.ModuleStatusInternal
		}
		return encoded, bus.ModuleStatusOK
	}
}

// DefaultPath resolves configuration inside the config process. Callers do not
// pass filesystem paths over the bus and never open the module's file.
func DefaultPath() (string, error) {
	if configured := os.Getenv("AIMEE_CONFIG_PATH"); configured != "" {
		return configured, nil
	}
	home := os.Getenv("AIMEE_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".config", "aimee")
	}
	return filepath.Join(home, "aimee.yaml"), nil
}

func NewDefaultHandler() (bus.ModuleHandler, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	store, err := NewStore(path)
	if err != nil {
		return nil, err
	}
	return NewHandler(store), nil
}

func handleRequest(store *Store, request configcontract.Request) configcontract.Response {
	var response configcontract.Response
	var err error
	switch request.Operation {
	case configcontract.OpValues:
		response.Values, err = store.Values()
	case configcontract.OpSnapshot:
		response.Values, response.Version, err = store.Snapshot()
	case configcontract.OpValue:
		response.Value, err = store.Value(request.Key)
	case configcontract.OpStringValue:
		var found bool
		response.Value, found, err = store.StringValue(request.Key)
		if err == nil && !found {
			err = errors.New("config key not found")
		}
	case configcontract.OpSetVersioned:
		err = store.SetVersioned(request.Key, request.Value, request.PreviousVersion)
	case configcontract.OpVersion:
		response.Version, err = store.Version(request.Key)
	case configcontract.OpTriggerRules:
		response.Rules, err = store.TriggerRules()
	case configcontract.OpWorkspaceAdd:
		var change WorkspaceMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.WorkspaceAdd(change)
		}
	case configcontract.OpWorkspaceRemove:
		var change WorkspaceMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.WorkspaceRemove(change.Path)
		}
	case configcontract.OpApplyRoundtablePreset:
		var preset RoundtablePreset
		if err = decodeRequestValue(request.Value, &preset); err == nil {
			err = store.ApplyRoundtablePreset(preset)
		}
	case configcontract.OpPersistDefaults:
		err = store.PersistDefaults()
	case configcontract.OpSetTypedFacts:
		var change TypedFactsMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.SetTypedFacts(change)
		}
	case configcontract.OpSetAPIHTTPListener:
		var change APIHTTPListenerMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.SetAPIHTTPListener(change)
		}
	case configcontract.OpSetModelConcurrency:
		var change ModelConcurrencyMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.SetModelConcurrency(change)
		}
	case configcontract.OpRemoveModelConcurrency:
		var change ModelConcurrencyMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.RemoveModelConcurrency(change.Model)
		}
	case configcontract.OpProfileCreate:
		var change configcontract.ProfileMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.ProfileCreate(change.Name)
		}
	case configcontract.OpProfilePresent:
		var change configcontract.ProfileMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			var present bool
			present, err = store.ProfilePresent(change.Name)
			if err == nil {
				response.Present = &present
			}
		}
	case configcontract.OpProfileList:
		var profiles []string
		profiles, err = store.ProfileList()
		if err == nil {
			response.Profiles = &profiles
		}
	case configcontract.OpProfileDelete:
		var change configcontract.ProfileMutation
		if err = decodeRequestValue(request.Value, &change); err == nil {
			err = store.ProfileDelete(change.Name)
		}
	default:
		return configcontract.Response{Code: "invalid_operation", Error: "unknown config operation"}
	}
	if err != nil {
		code := "invalid"
		switch {
		case strings.Contains(err.Error(), "unknown config key") ||
			strings.Contains(err.Error(), "not found"):
			code = "not_found"
		case strings.Contains(err.Error(), "version conflict"):
			code = "conflict"
		case strings.Contains(err.Error(), "already exists"):
			code = "exists"
		case strings.Contains(err.Error(), "registry full"):
			code = "full"
		}
		return configcontract.Response{Code: code, Error: err.Error()}
	}
	response.OK = true
	return response
}

func decodeRequestValue(value any, out any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("invalid operation value")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return errors.New("invalid operation value")
	}
	return nil
}
