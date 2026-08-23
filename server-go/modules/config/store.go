package config

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	configcontract "github.com/RakuenSoftware/aimee-module-config/server-go/config"
	"go.yaml.in/yaml/v3"
)

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func (s *Store) profileRoot() string {
	root := filepath.Dir(s.path)
	if filepath.Base(filepath.Dir(root)) == "profiles" {
		root = filepath.Dir(filepath.Dir(root))
	}
	return filepath.Join(root, "profiles")
}

func (s *Store) profileConfigPath(name string) (string, error) {
	if !profileNamePattern.MatchString(name) || name == "." || name == ".." {
		return "", errors.New("invalid profile name")
	}
	return filepath.Join(s.profileRoot(), name, "aimee.yaml"), nil
}

// ProfileCreate is the only implementation of a profile's configuration
// bootstrap. Native callers request it over the event bus and never construct
// or write configuration documents themselves.
func (s *Store) ProfileCreate(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.profileConfigPath(name)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	document := []byte("# Created by the Aimee config module.\nprovider: claude\nguardrail_mode: approve\n")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, err = file.Write(document); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (s *Store) ProfilePresent(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.profileConfigPath(name)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func (s *Store) ProfileList() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	root := s.profileRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !profileNamePattern.MatchString(entry.Name()) {
			continue
		}
		profiles = append(profiles, entry.Name())
	}
	return profiles, nil
}

func (s *Store) ProfileDelete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	profilePath, err := s.profileConfigPath(name)
	if err != nil {
		return err
	}
	if filepath.Clean(profilePath) == filepath.Clean(s.path) {
		return errors.New("cannot delete active profile")
	}
	root := filepath.Dir(profilePath)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("profile not found")
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("profile is not a directory")
	}
	return os.RemoveAll(root)
}

type Store struct {
	path     string
	mu       sync.Mutex
	lastGood *yaml.Node
}

// defaultsJSON is generated once from the last native configuration release's
// public accessor contract. It pins every shipped scalar/string default while
// the implementation is replaced by this pure-Go module.
//
//go:embed defaults.json
var defaultsJSON []byte

// metadataJSON is the public mutation/documentation schema that moved with the
// implementation. Keeping it here prevents Aimee callers from rebuilding a
// second allowlist from native layouts.
//
//go:embed metadata.json
var metadataJSON []byte

var declaredDefaults = func() map[string]any {
	var values map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(defaultsJSON)))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		panic(fmt.Sprintf("decode embedded config defaults: %v", err))
	}
	return values
}()

type fieldMetadata struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Group string `json:"group"`
}

var declaredFields = func() map[string]fieldMetadata {
	var metadata struct {
		Version int             `json:"version"`
		Fields  []fieldMetadata `json:"fields"`
	}
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil || metadata.Version != 1 {
		panic("decode embedded config metadata")
	}
	fields := make(map[string]fieldMetadata, len(metadata.Fields))
	for _, field := range metadata.Fields {
		if field.Key == "" || field.Type == "" {
			panic("invalid embedded config field metadata")
		}
		fields[field.Key] = field
	}
	return fields
}()

// Credentials are runtime-secret capabilities, not configuration values. They
// may still occur in an older YAML file during migration, but no read, snapshot,
// default persistence, or mutation operation may expose or rewrite them.
var secretKeys = map[string]struct{}{
	"db2_url": {}, "search_tavily_api_key": {}, "proxy_token": {},
	"ingress_trusted_proxy_secret": {}, "kb_api_bearer_token": {},
	"telemetry_metrics_token": {}, "kb_client_bearer_token": {},
	"server_api_bearer_token": {}, "trigger_auth_token": {},
	"kb_curator_provider_api_key": {}, "embedder_api_key": {},
	"synthesis_api_key": {}, "server_api_bearer_tokens_extra": {},
}

// YAML keeps its operator-facing section names. Native and Go callers keep the
// stable accessor names that predate the extraction. Only genuinely irregular
// mappings belong here; ordinary dotted keys are normalized mechanically.
var lookupAliases = map[string]string{
	"kb_typed_facts_enabled":                        "typed_facts_enabled",
	"kb_typed_facts_auto_promote":                   "kb_typed_facts_auto_promote_enabled",
	"kb_typed_facts_promote_threshold":              "kb_typed_facts_promote_threshold",
	"intelligence_calibrate_enabled":                "calibration_enabled",
	"intelligence_calibrate_command":                "calibration_command",
	"intelligence_calibrate_buckets":                "calibration_buckets",
	"intelligence_calibrate_prior_alpha0":           "calibration_prior_alpha0",
	"intelligence_calibrate_prior_beta0":            "calibration_prior_beta0",
	"intelligence_calibrate_credible_delta":         "calibration_credible_delta",
	"intelligence_calibrate_conformal_window":       "calibration_conformal_window",
	"intelligence_calibrate_conformal_epsilon":      "calibration_conformal_epsilon",
	"intelligence_synthesize_mdl_tiebreak_enabled":  "kb_mdl_tiebreak_enabled",
	"intelligence_synthesize_synthesize_n_attempts": "kb_synthesize_n_attempts",
	"intelligence_synthesize_reflection_shadow":     "kb_reflection_synthesis_shadow",
	"intelligence_synthesize_synthesize_command":    "kb_synthesize_command",
	"session_virtual_context_enabled":               "virtual_context_enabled",
	"session_virtual_context_assembly_budget":       "virtual_context_assembly_budget",
	"kb_mining_failure_learning_enabled":            "kb_mining_failure_learning_enabled",
	"aimee_api_http_port":                           "server_api_http_port",
	"aimee_api_tls_port":                            "server_api_tls_port",
	"aimee_api_mtls":                                "server_api_mtls",
	"aimee_api_mtls_client_ca":                      "server_api_mtls_client_ca",
	"aimee_api_bearer_token":                        "server_api_bearer_token",
	"aimee_api_bearer_tokens_extra":                 "server_api_bearer_tokens_extra",
	"aimee_api_rate_limit_per_min":                  "server_api_rate_limit_per_min",
	"aimee_api_max_event_streams":                   "server_api_max_event_streams",
	"aimee_api_cli_session_forwarding":              "server_api_cli_session_forwarding",
	"aimee_api_remote_writes":                       "server_api_remote_writes",
	"aimee_api_client_transport":                    "server_api_client_transport",
}

func publicKey(key string) bool {
	_, secret := secretKeys[normalizeLookupKey(key)]
	return !secret
}

func publicValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, child := range typed {
			if publicKey(key) {
				clean[key] = publicValue(child)
			}
		}
		return clean
	case []any:
		clean := make([]any, len(typed))
		for i := range typed {
			clean[i] = publicValue(typed[i])
		}
		return clean
	default:
		return value
	}
}

// MaxTriggerRules keeps the human-editable Go registry aligned with the
// long-standing C config schema. More rules than this are almost certainly an
// accidental duplicate/import and would make every scheduler scan expensive.
const MaxTriggerRules = configcontract.MaxTriggerRules

// TriggerRule is the persisted trigger_rules shape exposed by the Workflows UI.
// Pipeline remains nested in YAML; callers never need to reinterpret flattened
// config keys or maintain a second trigger configuration.
type TriggerRule = configcontract.TriggerRule
type TriggerPipeline = configcontract.TriggerPipeline

func (s *Store) TriggerRules() ([]TriggerRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.read()
	if err != nil {
		return nil, err
	}
	node := mappingChild(root, "trigger_rules", false)
	if node == nil {
		return []TriggerRule{}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, errors.New("trigger_rules must be a sequence")
	}
	var rules []TriggerRule
	if err := node.Decode(&rules); err != nil {
		return nil, fmt.Errorf("decode trigger_rules: %w", err)
	}
	if len(rules) > MaxTriggerRules {
		return nil, fmt.Errorf("trigger_rules has %d rules; maximum is %d", len(rules), MaxTriggerRules)
	}
	for i := range rules {
		if rules[i].Source == "" {
			return nil, fmt.Errorf("trigger_rules[%d].source is required", i)
		}
		if rules[i].Mode == "" {
			rules[i].Mode = "autonomous"
		}
		if rules[i].Mode != "autonomous" && rules[i].Mode != "interactive" {
			return nil, fmt.Errorf("trigger_rules[%d].mode must be autonomous or interactive", i)
		}
		if rules[i].Source == "watch-dir" || rules[i].Source == "proposals" {
			if rules[i].Event == "" {
				rules[i].Event = "docs/proposals/pending"
			}
			if !confinedTriggerDirectory(rules[i].Event) {
				return nil, fmt.Errorf("trigger_rules[%d].event must be a repository-relative directory", i)
			}
			if strings.HasPrefix(strings.TrimSpace(rules[i].Schedule), "-") {
				return nil, fmt.Errorf("trigger_rules[%d].schedule cannot start with '-'", i)
			}
			if rules[i].Pipeline.Template == "" || rules[i].Pipeline.Workspace == "" {
				return nil, fmt.Errorf("trigger_rules[%d] needs pipeline.template and pipeline.workspace", i)
			}
			if !validTriggerWorkspace(rules[i].Pipeline.Workspace) {
				return nil, fmt.Errorf("trigger_rules[%d].pipeline.workspace must be an absolute server path", i)
			}
			if rules[i].Pipeline.MaxSpendUSD < 0 || math.IsNaN(rules[i].Pipeline.MaxSpendUSD) || math.IsInf(rules[i].Pipeline.MaxSpendUSD, 0) {
				return nil, fmt.Errorf("trigger_rules[%d].pipeline.max_spend_usd must be finite and non-negative", i)
			}
		}
	}
	return rules, nil
}

func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("config path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: abs}, nil
}

var policyDefaults = map[string]any{
	"trigger.max_concurrent":            2,
	"trigger.scan_interval_secs":        5,
	"autonomy.auto_resume_cap_parks":    true,
	"autonomy.max_wall_secs":            1800,
	"autonomy.max_turns":                300,
	"autonomy.max_resumes":              50,
	"autonomy.stale_abandon_secs":       3600,
	"autonomy.concurrency":              5,
	"autonomy.per_workflow_concurrency": 1,
	"autonomy.delegate_pending_secs":    120,
}

var configurableTypes = map[string]string{
	"trigger.max_concurrent": "int", "autonomy.auto_resume_cap_parks": "bool",
	"trigger.scan_interval_secs": "int",
	"autonomy.max_wall_secs":     "int", "autonomy.max_turns": "int",
	"autonomy.max_resumes": "int", "autonomy.stale_abandon_secs": "int",
	"autonomy.concurrency":              "int",
	"autonomy.per_workflow_concurrency": "int",
	"autonomy.delegate_pending_secs":    "int",
}

type intBounds struct{ min, max int64 }

var configurableIntBounds = map[string]intBounds{
	"autonomy.delegate_pending_secs": {min: 2, max: 3600},
}

// The write-role wall floor. A write dispatch reserves time for the mandatory
// repository verifier and refuses outright below one viable model-call window, so
// under this many seconds every implement attempt refuses before it starts: the
// stage can never finish, no matter how often it retries. Rejecting the value
// when it is set says so up front instead of leaving it to be inferred from
// attempts dying.
//
// These two components are the engine's ALREADY-SHIPPED delegateWriteVerifyReserve
// and delegateWriteMinRunBudget, spelled out so the floor reads as what it is:
// the arithmetic consequence of existing behaviour, not a new policy number. The
// proposal that asked for this check excludes choosing how long a delegate may
// run, and this does not choose that — it rejects only the caps under which the
// shipped code already guarantees no attempt can finish.
//
// Duplicated rather than imported because internal/engine already imports this
// package; TestWriteRoleWallFloorMatchesConfigBound there fails if they drift.
const (
	writeVerifyReserveSecs = 300 // engine delegateWriteVerifyReserve (5m)
	writeMinRunSecs        = 60  // engine delegateWriteMinRunBudget (1m)

	MinAutonomyMaxWallSecs = configcontract.MinAutonomyMaxWallSecs
)

func (s *Store) Values() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make(map[string]any)
	flatten(root, "", out)
	for key := range out {
		if key != "trigger_rules" {
			if _, allowed := configurableTypes[key]; !allowed {
				delete(out, key)
			}
		}
	}
	for key, value := range policyDefaults {
		if _, ok := out[key]; !ok {
			out[key] = value
		}
	}
	return out, nil
}

// EffectiveValues returns the complete caller-side snapshot. Keys use the
// stable accessor spelling (dots and dashes normalized to underscores), so a
// native getter and a Go caller observe the same value without sharing a
// native struct layout. Operator-authored YAML wins over embedded defaults.
func (s *Store) EffectiveValues() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.read()
	if err != nil {
		return nil, err
	}
	return s.effectiveValuesLocked(root), nil
}

// Snapshot binds the effective values and document version to one locked read,
// so a reload client can never cache values from one write with the version of
// another.
func (s *Store) Snapshot() (map[string]any, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.read()
	if err != nil {
		return nil, "", err
	}
	return s.effectiveValuesLocked(root), configNodeVersion(root), nil
}

func (s *Store) effectiveValuesLocked(root *yaml.Node) map[string]any {
	out := make(map[string]any, len(declaredDefaults)+64)
	for key, value := range declaredDefaults {
		if publicKey(key) {
			out[key] = value
		}
	}
	raw := make(map[string]any)
	flatten(root, "", raw)
	for key, value := range raw {
		normalized := normalizeLookupKey(key)
		if alias, ok := lookupAliases[normalized]; ok {
			normalized = alias
		}
		if publicKey(normalized) {
			out[normalized] = callerContractValue(normalized, value)
		}
	}
	applyEnvironmentOverrides(out)
	if value, ok := out["db1_path"].(string); !ok || value == "" {
		out["db1_path"] = filepath.Join(filepath.Dir(s.path), "aimee.db")
	}
	// Structured callers sometimes need the complete section as well as its
	// individual flattened fields (for example sandbox policy and registries).
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		var value any
		if root.Content[i+1].Decode(&value) == nil && publicKey(root.Content[i].Value) {
			out[normalizeLookupKey(root.Content[i].Value)] = publicValue(value)
		}
	}
	projectLegacyStructures(out)
	return out
}

// callerContractValue retains the numeric enum contract exposed by the public
// accessors while YAML keeps its operator-facing strings. These conversions
// used to live in the native parser; moving the parser means they belong here.
func callerContractValue(key string, value any) any {
	text, isString := value.(string)
	if !isString {
		return value
	}
	switch key {
	case "server_api_mtls":
		switch text {
		case "required":
			return 2
		case "optional":
			return 1
		default:
			return 0
		}
	case "server_api_remote_writes":
		switch text {
		case "full":
			return 2
		case "data":
			return 1
		default:
			return 0
		}
	}
	return value
}

// These two variables are deployment truth in the established configuration
// contract. The config module receives the same container environment as its
// daemon and projects the overrides into the event-bus snapshot.
func applyEnvironmentOverrides(out map[string]any) {
	if value, present := os.LookupEnv("AIMEE_API_MTLS"); present && value != "" {
		switch value {
		case "required":
			out["server_api_mtls"] = 2
		case "optional":
			out["server_api_mtls"] = 1
		case "off":
			out["server_api_mtls"] = 0
		}
	}
	if value, present := os.LookupEnv("AIMEE_API_REMOTE_WRITES"); present && value != "" {
		switch value {
		case "full":
			out["server_api_remote_writes"] = 2
		case "data":
			out["server_api_remote_writes"] = 1
		case "off":
			out["server_api_remote_writes"] = 0
		}
	}
}

func projectLegacyStructures(out map[string]any) {
	if raw, ok := out["workspaces"].([]any); ok {
		existingProviders, _ := out["workspace_providers"].([]any)
		existingRemotes, _ := out["workspace_vcs_remote"].([]any)
		existingHeads, _ := out["workspace_vcs_head"].([]any)
		paths := make([]any, 0, len(raw))
		providers := make([]any, 0, len(raw))
		remotes := make([]any, 0, len(raw))
		heads := make([]any, 0, len(raw))
		for index, item := range raw {
			pathValue, provider, remote, head := "", "", "", ""
			if index < len(existingProviders) {
				provider, _ = existingProviders[index].(string)
			}
			if index < len(existingRemotes) {
				remote, _ = existingRemotes[index].(string)
			}
			if index < len(existingHeads) {
				head, _ = existingHeads[index].(string)
			}
			switch value := item.(type) {
			case string:
				pathValue = value
			case map[string]any:
				pathValue, _ = value["path"].(string)
				if field, present := value["provider"].(string); present {
					provider = field
				}
				if field, present := value["remote"].(string); present {
					remote = field
				}
				if field, present := value["head"].(string); present {
					head = field
				}
			}
			if pathValue == "" {
				continue
			}
			paths = append(paths, pathValue)
			providers = append(providers, provider)
			remotes = append(remotes, remote)
			heads = append(heads, head)
		}
		out["workspaces"] = paths
		out["workspace_providers"] = providers
		out["workspace_vcs_remote"] = remotes
		out["workspace_vcs_head"] = heads
		out["workspace_count"] = len(paths)
	}
	if rules, ok := out["trigger_rules"].([]any); ok {
		out["trigger_rule_count"] = len(rules)
	}
	if servers, ok := out["lsp_servers"].([]any); ok {
		out["lsp_server_count"] = len(servers)
	}
	if memory, ok := out["memory"].(map[string]any); ok {
		if dispositions, ok := memory["dispositions"].(map[string]any); ok {
			rows := make([]any, 0, len(dispositions))
			for name, value := range dispositions {
				rows = append(rows, map[string]any{"name": name, "value": value, "source": 1})
			}
			slices.SortFunc(rows, func(a, b any) int {
				return strings.Compare(a.(map[string]any)["name"].(string), b.(map[string]any)["name"].(string))
			})
			out["dispositions"] = rows
			out["disposition_count"] = len(rows)
			out["disposition_global_count"] = len(rows)
		}
	}
	if charter, ok := out["charter"].(map[string]any); ok {
		for _, field := range []string{"safety_axioms", "hard_constraints", "values", "tone_boundaries"} {
			if values, ok := charter[field].([]any); ok {
				out["charter_"+field] = values
				out["charter_"+field+"_count"] = len(values)
			}
		}
		if value, ok := charter["working_profile_drift_limit"]; ok {
			out["charter_working_profile_drift_limit"] = value
		}
	}
	if identity, ok := out["identity"].(map[string]any); ok {
		if injection, ok := identity["working_profile_injection"].(map[string]any); ok {
			if fields, ok := injection["fields"].([]any); ok {
				out["identity_working_profile_injection_fields"] = fields
				out["identity_working_profile_injection_fields_count"] = len(fields)
			}
		}
	}
}

func (s *Store) Value(key string) (any, error) {
	var values map[string]any
	var err error
	if _, policyKey := configurableTypes[key]; policyKey {
		values, err = s.Values()
		if err != nil {
			return nil, err
		}
		value, ok := values[key]
		if !ok {
			return nil, fmt.Errorf("unknown config key %q", key)
		}
		return value, nil
	}
	values, err = s.EffectiveValues()
	if err != nil {
		return nil, err
	}
	value, ok := values[normalizeLookupKey(key)]
	if !ok {
		return nil, fmt.Errorf("unknown config key %q", key)
	}
	return value, nil
}

func normalizeLookupKey(key string) string {
	key = strings.ReplaceAll(key, ".", "_")
	return strings.ReplaceAll(key, "-", "_")
}

func (s *Store) Int(key string, fallback int) int {
	value, ok, err := s.IntValue(key)
	if err != nil || !ok {
		return fallback
	}
	return value
}

func (s *Store) IntValue(key string) (int, bool, error) {
	value, err := s.Value(key)
	if err != nil {
		if strings.Contains(err.Error(), "unknown config key") {
			return 0, false, nil
		}
		return 0, false, err
	}
	switch n := value.(type) {
	case int:
		return n, true, nil
	case int64:
		return int(n), true, nil
	case uint64:
		return int(n), true, nil
	case float64:
		if n == float64(int(n)) {
			return int(n), true, nil
		}
	}
	return 0, false, fmt.Errorf("config key %q is not an integer", key)
}

func (s *Store) Bool(key string, fallback bool) bool {
	value, ok, err := s.BoolValue(key)
	if err != nil || !ok {
		return fallback
	}
	return value
}

func (s *Store) BoolValue(key string) (bool, bool, error) {
	value, err := s.Value(key)
	if err != nil {
		if strings.Contains(err.Error(), "unknown config key") {
			return false, false, nil
		}
		return false, false, err
	}
	b, ok := value.(bool)
	if !ok {
		return false, false, fmt.Errorf("config key %q is not boolean", key)
	}
	return b, true, nil
}

// StringValue reads a scalar string without adding it to the Workflows UI's
// mutable policy allowlist. Internal control-plane modules use it for existing
// product configuration such as roundtable.default.
func (s *Store) StringValue(key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.read()
	if err != nil {
		return "", false, err
	}
	values := make(map[string]any)
	flatten(root, "", values)
	value, ok := values[key]
	if !ok {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("config key %q is not a string", key)
	}
	return text, true, nil
}

func (s *Store) Set(key string, value any) error {
	return s.SetVersioned(key, value, "")
}

func (s *Store) SetVersioned(key string, value any, previousVersion string) error {
	if !publicKey(key) {
		return errors.New("credential keys are owned by the runtime secret store")
	}
	if err := validateKeyValue(key, value); err != nil {
		return err
	}
	// The bus decoder preserves JSON numbers so callers do not lose integer
	// precision. Normalize integral values before YAML encoding; yaml would
	// otherwise persist json.Number as a quoted string.
	if encoded, ok := value.(json.Number); ok {
		if parsed, err := encoded.Int64(); err == nil {
			value = parsed
		} else {
			parsed, floatErr := encoded.Float64()
			if floatErr != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
				return fmt.Errorf("%s must be numeric", key)
			}
			value = parsed
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	if previousVersion != "" {
		current := configNodeVersion(mappingChild(root, key, false))
		if current != previousVersion {
			return fmt.Errorf("config version conflict (current %s)", current)
		}
	}
	parts := strings.Split(key, ".")
	if len(parts) == 0 {
		return errors.New("invalid config key")
	}
	node := root
	for _, part := range parts[:len(parts)-1] {
		if !safePart(part) {
			return errors.New("invalid config key")
		}
		node = mappingChild(node, part, true)
		if node == nil || node.Kind != yaml.MappingNode {
			return fmt.Errorf("config path %q conflicts with a non-mapping value", part)
		}
	}
	leaf := parts[len(parts)-1]
	if !safePart(leaf) {
		return errors.New("invalid config key")
	}
	valueNode := &yaml.Node{}
	if err := valueNode.Encode(value); err != nil {
		return fmt.Errorf("encode config value: %w", err)
	}
	setMappingChild(node, leaf, valueNode)
	return s.writeLocked(root)
}

const maxWorkspaces = 64

const maxModelConcurrencyEntries = 64

type TypedFactsMutation = configcontract.TypedFactsMutation
type APIHTTPListenerMutation = configcontract.APIHTTPListenerMutation
type ModelConcurrencyMutation = configcontract.ModelConcurrencyMutation

type modelConcurrencyEntry struct {
	Key   string `yaml:"key" json:"key"`
	Limit int    `yaml:"limit" json:"limit"`
}

func (s *Store) SetTypedFacts(change TypedFactsMutation) error {
	if change.PromoteThreshold != nil && *change.PromoteThreshold <= 0 {
		return errors.New("typed facts promote threshold must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	kb := mappingChild(root, "kb", true)
	if kb == nil || kb.Kind != yaml.MappingNode {
		return errors.New("config path \"kb\" conflicts with a non-mapping value")
	}
	typedFacts := mappingChild(kb, "typed_facts", true)
	if typedFacts == nil || typedFacts.Kind != yaml.MappingNode {
		return errors.New("config path \"kb.typed_facts\" conflicts with a non-mapping value")
	}
	if change.Enabled != nil {
		if err := setEncoded(typedFacts, "enabled", *change.Enabled); err != nil {
			return err
		}
		// The nested KB setting is canonical. Retire the legacy root alias once
		// this grouped operation takes ownership so parse order cannot make the
		// persisted document ambiguous.
		deleteMappingChild(root, "typed_facts_enabled")
	}
	if change.AutoPromote != nil {
		if err := setEncoded(typedFacts, "auto_promote", *change.AutoPromote); err != nil {
			return err
		}
	}
	if change.PromoteThreshold != nil {
		if err := setEncoded(typedFacts, "promote_threshold", *change.PromoteThreshold); err != nil {
			return err
		}
	}
	return s.writeLocked(root)
}

func (s *Store) SetAPIHTTPListener(change APIHTTPListenerMutation) error {
	if change.HTTPPort < 0 || change.HTTPPort > 65535 || change.RateLimitPerMin <= 0 {
		return errors.New("invalid API HTTP listener settings")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	if err := setEncoded(root, "server_api_http_port", change.HTTPPort); err != nil {
		return err
	}
	if err := setEncoded(root, "server_api_rate_limit_per_min", change.RateLimitPerMin); err != nil {
		return err
	}
	return s.writeLocked(root)
}

func (s *Store) SetModelConcurrency(change ModelConcurrencyMutation) error {
	if strings.TrimSpace(change.Model) == "" || change.Model != strings.TrimSpace(change.Model) ||
		change.Limit <= 0 {
		return errors.New("invalid model concurrency setting")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	var entries []modelConcurrencyEntry
	if node := mappingChild(root, "concurrency_per_model", false); node != nil {
		if err := node.Decode(&entries); err != nil {
			return errors.New("concurrency_per_model must be a model/limit list")
		}
	}
	for i := range entries {
		if entries[i].Key == change.Model {
			entries[i].Limit = change.Limit
			if err := setEncoded(root, "concurrency_per_model", entries); err != nil {
				return err
			}
			if err := setEncoded(root, "concurrency_per_model_count", len(entries)); err != nil {
				return err
			}
			return s.writeLocked(root)
		}
	}
	if len(entries) >= maxModelConcurrencyEntries {
		return errors.New("model concurrency registry full")
	}
	entries = append(entries, modelConcurrencyEntry{Key: change.Model, Limit: change.Limit})
	if err := setEncoded(root, "concurrency_per_model", entries); err != nil {
		return err
	}
	if err := setEncoded(root, "concurrency_per_model_count", len(entries)); err != nil {
		return err
	}
	return s.writeLocked(root)
}

func (s *Store) RemoveModelConcurrency(model string) error {
	if strings.TrimSpace(model) == "" || model != strings.TrimSpace(model) {
		return errors.New("model is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	var entries []modelConcurrencyEntry
	if node := mappingChild(root, "concurrency_per_model", false); node != nil {
		if err := node.Decode(&entries); err != nil {
			return errors.New("concurrency_per_model must be a model/limit list")
		}
	}
	kept := entries[:0]
	for _, entry := range entries {
		if entry.Key != model {
			kept = append(kept, entry)
		}
	}
	if err := setEncoded(root, "concurrency_per_model", kept); err != nil {
		return err
	}
	if err := setEncoded(root, "concurrency_per_model_count", len(kept)); err != nil {
		return err
	}
	return s.writeLocked(root)
}

type WorkspaceMutation struct {
	Path     string `json:"path"`
	Provider string `json:"provider,omitempty"`
	Remote   string `json:"remote,omitempty"`
	Head     string `json:"head,omitempty"`
}

type RoundtablePreset struct {
	Models                       []string `json:"models"`
	Personas                     []string `json:"personas"`
	MinSuccessful                int      `json:"min_successful"`
	MaxCostUSD                   float64  `json:"max_cost_usd"`
	MaxRounds                    int      `json:"max_rounds"`
	ConvergeThreshold            int      `json:"converge_threshold"`
	DeadlineMS                   int      `json:"deadline_ms"`
	Turns                        string   `json:"turns,omitempty"`
	PipelineDoneBar              string   `json:"pipeline_done_bar,omitempty"`
	PipelineMaxPasses            int      `json:"pipeline_max_passes"`
	PipelineMaxAttemptsPerPass   int      `json:"pipeline_max_attempts_per_pass"`
	PipelineMaxCostUSD           float64  `json:"pipeline_max_cost_usd"`
	PipelineMaxTotalCostUSD      float64  `json:"pipeline_max_total_cost_usd"`
	PipelineGateTTLH             int      `json:"pipeline_gate_ttl_h"`
	PipelineParkedReleasesSlot   int      `json:"pipeline_parked_releases_slot"`
	PipelineUnknownContextTokens int      `json:"pipeline_unknown_context_tokens"`
	Name                         string   `json:"name"`
}

func decodeStringSlice(root *yaml.Node, key string) ([]string, error) {
	node := mappingChild(root, key, false)
	if node == nil {
		return []string{}, nil
	}
	var values []string
	if err := node.Decode(&values); err != nil {
		return nil, fmt.Errorf("%s must be a string list: %w", key, err)
	}
	return values, nil
}

func setEncoded(root *yaml.Node, key string, value any) error {
	node := &yaml.Node{}
	if err := node.Encode(value); err != nil {
		return err
	}
	setMappingChild(root, key, node)
	return nil
}

func aligned(values []string, n int) []string {
	if len(values) >= n {
		return values[:n]
	}
	return append(values, make([]string, n-len(values))...)
}

func (s *Store) WorkspaceAdd(change WorkspaceMutation) error {
	if !filepath.IsAbs(change.Path) || strings.IndexFunc(change.Path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("workspace path must be an absolute clean path")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	paths, err := decodeStringSlice(root, "workspaces")
	if err != nil {
		return err
	}
	providers, err := decodeStringSlice(root, "workspace_providers")
	if err != nil {
		return err
	}
	remotes, err := decodeStringSlice(root, "workspace_vcs_remote")
	if err != nil {
		return err
	}
	heads, err := decodeStringSlice(root, "workspace_vcs_head")
	if err != nil {
		return err
	}
	providers, remotes, heads = aligned(providers, len(paths)), aligned(remotes, len(paths)), aligned(heads, len(paths))
	for i, registered := range paths {
		if registered != change.Path {
			continue
		}
		if change.Provider != "" {
			providers[i] = change.Provider
			if change.Provider == "shared" {
				providers[i] = ""
			}
		}
		if change.Remote != "" {
			remotes[i] = change.Remote
		}
		if change.Head != "" {
			heads[i] = change.Head
		}
		if err := setWorkspaceLists(root, paths, providers, remotes, heads); err != nil {
			return err
		}
		if err := s.writeLocked(root); err != nil {
			return err
		}
		return errors.New("workspace already exists")
	}
	if len(paths) >= maxWorkspaces {
		keptPaths, keptProviders, keptRemotes, keptHeads := []string{}, []string{}, []string{}, []string{}
		for i, registered := range paths {
			if _, statErr := os.Stat(registered); errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			keptPaths = append(keptPaths, registered)
			keptProviders = append(keptProviders, providers[i])
			keptRemotes = append(keptRemotes, remotes[i])
			keptHeads = append(keptHeads, heads[i])
		}
		paths, providers, remotes, heads = keptPaths, keptProviders, keptRemotes, keptHeads
	}
	if len(paths) >= maxWorkspaces {
		return errors.New("workspace registry full")
	}
	provider := change.Provider
	if provider == "shared" {
		provider = ""
	}
	paths = append(paths, change.Path)
	providers = append(providers, provider)
	remotes = append(remotes, change.Remote)
	heads = append(heads, change.Head)
	if err := setWorkspaceLists(root, paths, providers, remotes, heads); err != nil {
		return err
	}
	return s.writeLocked(root)
}

func setWorkspaceLists(root *yaml.Node, paths, providers, remotes, heads []string) error {
	for key, value := range map[string]any{
		"workspaces": paths, "workspace_providers": providers,
		"workspace_vcs_remote": remotes, "workspace_vcs_head": heads,
		"workspace_count": len(paths),
	} {
		if err := setEncoded(root, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) WorkspaceRemove(workspacePath string) error {
	if workspacePath == "" {
		return errors.New("workspace path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	paths, err := decodeStringSlice(root, "workspaces")
	if err != nil {
		return err
	}
	providers, _ := decodeStringSlice(root, "workspace_providers")
	remotes, _ := decodeStringSlice(root, "workspace_vcs_remote")
	heads, _ := decodeStringSlice(root, "workspace_vcs_head")
	providers, remotes, heads = aligned(providers, len(paths)), aligned(remotes, len(paths)), aligned(heads, len(paths))
	for i, registered := range paths {
		if registered != workspacePath {
			continue
		}
		paths = append(paths[:i], paths[i+1:]...)
		providers = append(providers[:i], providers[i+1:]...)
		remotes = append(remotes[:i], remotes[i+1:]...)
		heads = append(heads[:i], heads[i+1:]...)
		if err := setWorkspaceLists(root, paths, providers, remotes, heads); err != nil {
			return err
		}
		return s.writeLocked(root)
	}
	return errors.New("workspace not found")
}

func (s *Store) ApplyRoundtablePreset(p RoundtablePreset) error {
	if p.Name == "" || len(p.Models) != len(p.Personas) || len(p.Models) > 32 {
		return errors.New("invalid roundtable preset")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	values := map[string]any{
		"ensemble_reference_models": p.Models, "ensemble_reference_personas": p.Personas,
		"ensemble_reference_count": len(p.Models), "ensemble_reference_persona_count": len(p.Personas),
		"ensemble_min_successful": p.MinSuccessful, "ensemble_max_cost_usd": p.MaxCostUSD,
		"roundtable_max_rounds": p.MaxRounds, "roundtable_converge_threshold": p.ConvergeThreshold,
		"roundtable_deadline_ms": p.DeadlineMS, "roundtable_pipeline_max_passes": p.PipelineMaxPasses,
		"roundtable_pipeline_max_attempts_per_pass":  p.PipelineMaxAttemptsPerPass,
		"roundtable_pipeline_max_cost_usd":           p.PipelineMaxCostUSD,
		"roundtable_pipeline_max_total_cost_usd":     p.PipelineMaxTotalCostUSD,
		"roundtable_pipeline_gate_ttl_h":             p.PipelineGateTTLH,
		"roundtable_pipeline_parked_releases_slot":   p.PipelineParkedReleasesSlot,
		"roundtable_pipeline_unknown_context_tokens": p.PipelineUnknownContextTokens,
		"roundtable_default":                         p.Name,
	}
	if p.Turns != "" {
		values["roundtable_turns"] = p.Turns
	}
	if p.PipelineDoneBar != "" {
		values["roundtable_pipeline_done_bar"] = p.PipelineDoneBar
	}
	for key, value := range values {
		if err := setEncoded(root, key, value); err != nil {
			return err
		}
	}
	return s.writeLocked(root)
}

func (s *Store) PersistDefaults() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.readStrict()
	if err != nil {
		return err
	}
	for key, value := range declaredDefaults {
		if !publicKey(key) {
			continue
		}
		if mappingChild(root, key, false) != nil {
			continue
		}
		if err := setEncoded(root, key, value); err != nil {
			return err
		}
	}
	return s.writeLocked(root)
}

func (s *Store) writeLocked(root *yaml.Node) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".aimee.yaml.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	encoder := yaml.NewEncoder(tmp)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	s.lastGood = cloneYAMLNode(root)
	return nil
}

func (s *Store) Version(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.read()
	if err != nil {
		return "", err
	}
	if key == "" {
		return configNodeVersion(root), nil
	}
	return configNodeVersion(mappingChild(root, key, false)), nil
}

func configNodeVersion(node *yaml.Node) string {
	if node == nil {
		node = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	content, _ := yaml.Marshal(node)
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func validateKeyValue(key string, value any) error {
	if key == "trigger_rules" {
		node := &yaml.Node{}
		if err := node.Encode(value); err != nil {
			return fmt.Errorf("encode trigger_rules: %w", err)
		}
		var rules []TriggerRule
		if err := node.Decode(&rules); err != nil {
			return fmt.Errorf("trigger_rules must be a list of rules: %w", err)
		}
		if len(rules) > MaxTriggerRules {
			return fmt.Errorf("trigger_rules has %d rules; maximum is %d", len(rules), MaxTriggerRules)
		}
		for i, rule := range rules {
			if rule.Source != "watch-dir" && rule.Source != "proposals" {
				return fmt.Errorf("trigger_rules[%d].source must be watch-dir or proposals", i)
			}
			if rule.Mode != "" && rule.Mode != "autonomous" && rule.Mode != "interactive" {
				return fmt.Errorf("trigger_rules[%d].mode must be autonomous or interactive", i)
			}
			event := rule.Event
			if event == "" {
				event = "docs/proposals/pending"
			}
			if !confinedTriggerDirectory(event) {
				return fmt.Errorf("trigger_rules[%d].event must be a repository-relative directory", i)
			}
			if strings.HasPrefix(strings.TrimSpace(rule.Schedule), "-") {
				return fmt.Errorf("trigger_rules[%d].schedule cannot start with '-'", i)
			}
			if rule.Pipeline.Template == "" || rule.Pipeline.Workspace == "" {
				return fmt.Errorf("trigger_rules[%d] needs pipeline.template and pipeline.workspace", i)
			}
			if !validTriggerWorkspace(rule.Pipeline.Workspace) {
				return fmt.Errorf("trigger_rules[%d].pipeline.workspace must be an absolute server path", i)
			}
			if rule.Pipeline.MaxSpendUSD < 0 || math.IsNaN(rule.Pipeline.MaxSpendUSD) || math.IsInf(rule.Pipeline.MaxSpendUSD, 0) {
				return fmt.Errorf("trigger_rules[%d].pipeline.max_spend_usd must be finite and non-negative", i)
			}
		}
		return nil
	}
	typeName, allowed := configurableTypes[key]
	if !allowed {
		normalized := normalizeLookupKey(key)
		if field, exists := declaredFields[normalized]; exists {
			return validateMetadataType(key, field.Type, value)
		}
		if declared, exists := declaredDefaults[normalized]; exists {
			return validateDeclaredType(key, declared, value)
		}
		root := strings.Split(key, ".")[0]
		if !strings.Contains(key, ".") || !editableStructuredRoots[root] {
			return fmt.Errorf("config key %q is not editable", key)
		}
		if !supportedConfigValue(value) {
			return fmt.Errorf("config key %q has an unsupported value", key)
		}
		return nil
	}
	switch typeName {
	case "int":
		n, ok := number(value)
		if !ok || n < 0 {
			return fmt.Errorf("%s must be a non-negative integer", key)
		}
		if bounds, bounded := configurableIntBounds[key]; bounded && (n < bounds.min || n > bounds.max) {
			return fmt.Errorf("%s must be between %d and %d", key, bounds.min, bounds.max)
		}
		if key == "autonomy.max_wall_secs" && n > 0 && n < MinAutonomyMaxWallSecs {
			return fmt.Errorf(
				"autonomy.max_wall_secs=%d is below the %d seconds a write-role delegate needs to run (%ds verifier reserve + %ds minimum run): every implement stage would refuse before starting, so no attempt could ever finish",
				n, MinAutonomyMaxWallSecs, writeVerifyReserveSecs, writeMinRunSecs)
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be boolean", key)
		}
	}
	return nil
}

func validateMetadataType(key, typeName string, value any) error {
	switch typeName {
	case "string", "string (off|safe|aggressive)":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", key)
		}
		if typeName != "string" && text != "off" && text != "safe" && text != "aggressive" {
			return fmt.Errorf("%s must be off, safe, or aggressive", key)
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be boolean", key)
		}
	case "int":
		if _, ok := number(value); !ok {
			return fmt.Errorf("%s must be an integer", key)
		}
	case "float":
		if !finiteNumber(value) {
			return fmt.Errorf("%s must be numeric", key)
		}
	default:
		return fmt.Errorf("config key %q has unsupported metadata type %q", key, typeName)
	}
	return nil
}

func finiteNumber(value any) bool {
	var numberValue float64
	switch typed := value.(type) {
	case int:
		numberValue = float64(typed)
	case int64:
		numberValue = float64(typed)
	case uint64:
		numberValue = float64(typed)
	case float64:
		numberValue = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return false
		}
		numberValue = parsed
	default:
		return false
	}
	return !math.IsNaN(numberValue) && !math.IsInf(numberValue, 0)
}

var editableStructuredRoots = map[string]bool{
	"autonomy": true, "auxiliary": true, "charter": true, "concurrency": true,
	"dogfood": true, "ensemble": true, "identity": true, "intelligence": true,
	"kb": true, "learning": true, "memory": true, "modules": true,
	"roundtable": true, "sandbox": true, "server": true, "transport": true,
	"trigger": true, "vault": true,
}

func supportedConfigValue(value any) bool {
	switch value.(type) {
	case nil, bool, string, json.Number, float64, int, int64, uint64,
		[]any, map[string]any, []TriggerRule:
		return true
	default:
		return false
	}
}

func validateDeclaredType(key string, declared, value any) error {
	switch declared.(type) {
	case string:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", key)
		}
	case json.Number, float64:
		switch value.(type) {
		case json.Number, float64, int, int64, uint64, bool:
		default:
			return fmt.Errorf("%s must be numeric or boolean", key)
		}
	}
	return nil
}

func confinedTriggerDirectory(directory string) bool {
	directory = strings.TrimSpace(directory)
	if strings.Contains(directory, "\\") || strings.IndexFunc(directory, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return false
	}
	clean := path.Clean(directory)
	return clean != "." && !path.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, "../")
}

func validTriggerWorkspace(workspace string) bool {
	return workspace == strings.TrimSpace(workspace) && filepath.IsAbs(workspace) &&
		strings.IndexFunc(workspace, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func number(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	case json.Number:
		parsed, err := n.Int64()
		return parsed, err == nil
	}
	return 0, false
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clone.Content[i] = cloneYAMLNode(child)
	}
	return &clone
}

// read serves the last fully parsed snapshot when an out-of-band edit is
// malformed. This preserves the live configuration contract: a broken reload
// is rejected without replacing the values already in use. Mutations use
// readStrict so they can never overwrite or silently repair an invalid file.
func (s *Store) read() (*yaml.Node, error) {
	root, err := s.readStrict()
	if err != nil && s.lastGood != nil {
		return cloneYAMLNode(s.lastGood), nil
	}
	return root, err
}

func (s *Store) readStrict() (*yaml.Node, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	content, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.lastGood = cloneYAMLNode(root)
		return root, nil
	}
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(document.Content) == 0 {
		s.lastGood = cloneYAMLNode(root)
		return root, nil
	}
	if document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config root must be a mapping")
	}
	root = document.Content[0]
	s.lastGood = cloneYAMLNode(root)
	return root, nil
}

func safePart(part string) bool {
	if part == "" {
		return false
	}
	for _, r := range part {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func mappingChild(node *yaml.Node, key string, create bool) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	if !create {
		return nil
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	return child
}

func setMappingChild(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func deleteMappingChild(node *yaml.Node, key string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

func flatten(node *yaml.Node, prefix string, out map[string]any) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if prefix != "" {
			key = prefix + "." + key
		}
		value := node.Content[i+1]
		if value.Kind == yaml.MappingNode {
			flatten(value, key, out)
			continue
		}
		var decoded any
		if value.Decode(&decoded) == nil {
			out[key] = decoded
		}
	}
}
