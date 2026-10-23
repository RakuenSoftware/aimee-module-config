package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RakuenSoftware/aimee-module-config/server-go/bus"
	configcontract "github.com/RakuenSoftware/aimee-module-config/server-go/config"
	"go.yaml.in/yaml/v3"
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

// The typed-fact master gate is retired, so `enabled` is not a settable option.
// A caller that still sends it must be REFUSED rather than have the field
// quietly dropped: silently accepting a request to disable a correctness feature
// and then not disabling it is how an operator ends up believing something about
// their deployment that is not true.
func TestHandlerRefusesRetiredTypedFactsEnabled(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	handler := NewHandler(store)
	// The bus status stays OK for an application-level rejection, so the RESPONSE
	// is what carries the verdict -- checking the status here would pass on a
	// request that was actually accepted.
	rejected := invoke(t, handler, configcontract.Request{
		Operation: configcontract.OpSetTypedFacts,
		Value: map[string]any{
			"auto_promote":      false,
			"enabled":           true,
			"promote_threshold": 5,
		},
	})
	if rejected.OK {
		t.Fatal("set-typed-facts accepted a retired `enabled` field")
	}
	// The two real knobs still work, so the refusal above is about `enabled` and
	// not about the operation being broken.
	accepted := invoke(t, handler, configcontract.Request{
		Operation: configcontract.OpSetTypedFacts,
		Value: map[string]any{
			"auto_promote":      false,
			"promote_threshold": 5,
		},
	})
	if !accepted.OK {
		t.Fatalf("set-typed-facts without `enabled` was refused: %+v", accepted)
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

// UseNumber on BOTH sides is part of the actual JSON bus contract: a test-side
// float64 decoder would itself lose the precision this boundary must preserve.
func invokeNumericWire(t *testing.T, handler bus.ModuleHandler, request configcontract.Request) configcontract.Response {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	reply, status := handler(bus.ModuleInvocation{StageID: configcontract.StageConfig}, body)
	if status != bus.ModuleStatusOK {
		t.Fatalf("module status %v", status)
	}
	var response configcontract.Response
	decoder := json.NewDecoder(bytes.NewReader(reply))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHandlerNestedTriggerBudgetRemainsNumeric(t *testing.T) {
	for _, budget := range []string{"1", "0", "1.25", "1e3"} {
		t.Run(budget, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aimee.yaml")
			store, err := NewStore(path)
			if err != nil {
				t.Fatal(err)
			}
			value := json.RawMessage(`[{"source":"watch-dir","event":"docs/proposals","schedule":"pinned","mode":"interactive","pipeline":{"template":"plan","workspace":"/srv/owned-repo","max_spend_usd":` + budget + `}}]`)
			response := invokeNumericWire(t, NewHandler(store), configcontract.Request{Operation: configcontract.OpSetVersioned, Key: "trigger_rules", Value: value})
			if !response.OK {
				t.Fatalf("actual handler rejected nested numeric budget: %+v", response)
			}
			reloaded, err := NewStore(path)
			if err != nil {
				t.Fatal(err)
			}
			rules := invokeNumericWire(t, NewHandler(reloaded), configcontract.Request{Operation: configcontract.OpTriggerRules})
			want, _ := json.Number(budget).Float64()
			if !rules.OK || len(rules.Rules) != 1 || rules.Rules[0].Pipeline.MaxSpendUSD != want {
				t.Fatalf("reloaded typed rule=%+v", rules)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var document yaml.Node
			if err := yaml.Unmarshal(content, &document); err != nil {
				t.Fatal(err)
			}
			node := mappingChild(document.Content[0], "trigger_rules", false).Content[0]
			node = mappingChild(mappingChild(node, "pipeline", false), "max_spend_usd", false)
			if node.Tag != "!!int" && node.Tag != "!!float" {
				t.Fatalf("budget persisted as %s: %s", node.Tag, content)
			}
		})
	}
}

func TestHandlerNestedJSONNumbersAndStringsKeepExactWireValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, _ := NewStore(path)
	payload := `{"integer":9007199254740993,"unsigned":18446744073709551615,"huge":184467440737095516160000000001,"fraction":0.123456789012345678901,"underflow":1e-400,"array":[1,1.25,"123",{"number":-2e3,"numeric_string":"9007199254740993"}],"boolean":true,"null":null}`
	response := invokeNumericWire(t, NewHandler(store), configcontract.Request{Operation: configcontract.OpSetVersioned, Key: "learning.number_fidelity", Value: json.RawMessage(payload)})
	if !response.OK {
		t.Fatalf("set=%+v", response)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	response = invokeNumericWire(t, NewHandler(reloaded), configcontract.Request{Operation: configcontract.OpSnapshot})
	if !response.OK {
		t.Fatalf("snapshot=%+v", response)
	}
	learning, ok := response.Values["learning"].(map[string]any)
	if !ok {
		t.Fatalf("learning=%#v", response.Values["learning"])
	}
	var want any
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(learning["number_fidelity"], want) {
		t.Fatalf("numeric/string fidelity lost: got=%#v want=%#v", learning["number_fidelity"], want)
	}
}

func TestHandlerInvalidNumericChangesAreAtomicAndDoNotCoerceStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, _ := NewStore(path)
	handler := NewHandler(store)
	if response := invokeNumericWire(t, handler, configcontract.Request{Operation: configcontract.OpSetVersioned, Key: "autonomy.max_turns", Value: 17}); !response.OK {
		t.Fatal(response)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`[{"source":"watch-dir","pipeline":{"template":"plan","workspace":"/srv/owned-repo","max_spend_usd":"1"}}]`, `[{"source":"watch-dir","pipeline":{"template":"plan","workspace":"/srv/owned-repo","max_spend_usd":1e999}}]`, `[{"source":"watch-dir","pipeline":{"template":"plan","workspace":"/srv/owned-repo","max_spend_usd":-1}}]`} {
		response := invokeNumericWire(t, handler, configcontract.Request{Operation: configcontract.OpSetVersioned, Key: "trigger_rules", Value: json.RawMessage(value)})
		if response.OK {
			t.Fatalf("invalid typed budget admitted: %s", value)
		}
	}
	response := invokeNumericWire(t, handler, configcontract.Request{Operation: configcontract.OpSetVersioned, Key: "autonomy.delegate_pending_secs", Value: json.Number("9007199254740993")})
	if response.OK {
		t.Fatal("published integer bound was weakened")
	}
	if err := store.Set("learning.invalid_number", map[string]any{"nested": []any{json.Number("01")}}); err == nil {
		t.Fatal("invalid programmatic JSON number accepted")
	}
	if _, status := handler(bus.ModuleInvocation{StageID: configcontract.StageConfig}, []byte(`{"operation":"set-versioned","key":"learning.invalid_number","value":{"nested":NaN}}`)); status != bus.ModuleStatusInvalidRequest {
		t.Fatal("invalid JSON number syntax admitted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected numeric mutation altered persisted configuration")
	}
}

func TestHandlerLargeScalarJSONIntegerDoesNotRoundThroughFloat(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store)
	set := invokeNumericWire(t, handler, configcontract.Request{Operation: configcontract.OpSetVersioned, Key: "learning.large_integer", Value: json.Number("18446744073709551615")})
	if !set.OK {
		t.Fatal(set)
	}
	get := invokeNumericWire(t, handler, configcontract.Request{Operation: configcontract.OpValue, Key: "learning.large_integer"})
	if !get.OK || get.Value != json.Number("18446744073709551615") {
		t.Fatalf("large integer rounded: %+v", get)
	}
}

func TestStoreEmptyJSONNumberUsesJSONZeroAndPlainEmptyStringStaysString(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("learning.empty_number", json.Number("")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("learning.empty_string", ""); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	response := invokeNumericWire(t, NewHandler(reloaded), configcontract.Request{Operation: configcontract.OpSnapshot})
	if !response.OK {
		t.Fatal(response)
	}
	learning := response.Values["learning"].(map[string]any)
	if learning["empty_number"] != json.Number("0") || learning["empty_string"] != "" {
		t.Fatalf("empty numeric/string semantics=%#v", learning)
	}
}
