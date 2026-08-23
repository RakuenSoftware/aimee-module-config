package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCanonicalNonZeroDefaults(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"calibration_enabled":         json.Number("1"),
		"calibration_buckets":         json.Number("10"),
		"calibration_prior_alpha0":    json.Number("2"),
		"demotion_enabled":            json.Number("1"),
		"demotion_window":             json.Number("64"),
		"bandit_exploration_fraction": json.Number("0.05"),
		"planner_budget_default":      json.Number("32"),
		"kb_mdl_tiebreak_enabled":     json.Number("1"),
		"kb_synthesize_n_attempts":    json.Number("3"),
		"calibration_prompt_version":  "v1",
		"calibration_model_version":   "beta-binomial-v1",
		"kb_fusion_mode":              "rrf",
	}
	for key, expected := range want {
		if got := values[key]; got != expected {
			t.Errorf("%s default=%#v, want %#v", key, got, expected)
		}
	}
}

func TestLegacyNestedSynthesisKeysProjectToCallerContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("intelligence:\n  synthesize:\n    mdl_tiebreak_enabled: false\n    synthesize_n_attempts: 7\n    reflection_shadow: true\n    synthesize_command: run-synth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"kb_mdl_tiebreak_enabled":        false,
		"kb_synthesize_n_attempts":       7,
		"kb_reflection_synthesis_shadow": true,
		"kb_synthesize_command":          "run-synth",
	} {
		if got := values[key]; got != want {
			t.Errorf("%s=%#v, want %#v", key, got, want)
		}
	}
}

func TestServerAPINestedDocumentProjectsPublicAccessorContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	document := "aimee:\n  api:\n    http_port: 8740\n    tls_port: 8743\n    mtls: optional\n    mtls_client_ca: /run/aimee/client-ca.pem\n    bearer_token: must-not-leak\n    bearer_tokens_extra: [also-secret]\n    rate_limit_per_min: 17\n    max_event_streams: 23\n    cli_session_forwarding: false\n    remote_writes: data\n    client_transport: http\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"server_api_http_port": 8740, "server_api_tls_port": 8743,
		"server_api_mtls": 1, "server_api_mtls_client_ca": "/run/aimee/client-ca.pem",
		"server_api_rate_limit_per_min": 17, "server_api_max_event_streams": 23,
		"server_api_cli_session_forwarding": false, "server_api_remote_writes": 1,
		"server_api_client_transport": "http",
	}
	for key, expected := range want {
		if got := values[key]; got != expected {
			t.Errorf("%s=%#v, want %#v", key, got, expected)
		}
	}
	for _, key := range []string{"aimee_api_bearer_token", "aimee_api_bearer_tokens_extra", "server_api_bearer_token", "server_api_bearer_tokens_extra"} {
		if _, present := values[key]; present {
			t.Errorf("credential %q leaked in snapshot", key)
		}
	}
}

func TestServerAPIDeploymentEnvironmentOverridesDocument(t *testing.T) {
	t.Setenv("AIMEE_API_MTLS", "required")
	t.Setenv("AIMEE_API_REMOTE_WRITES", "full")
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("aimee:\n  api:\n    mtls: optional\n    remote_writes: off\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(path)
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if values["server_api_mtls"] != 2 || values["server_api_remote_writes"] != 2 {
		t.Fatalf("deployment overrides not projected: mtls=%v remote_writes=%v", values["server_api_mtls"], values["server_api_remote_writes"])
	}
}

func TestStructuredRegistriesProjectCountsAndAlignedWorkspaceFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	document := "workspaces:\n  - /one\n  - path: /two\n    provider: git\n    remote: origin\n    head: main\ntrigger_rules:\n  - source: cron\nlsp_servers:\n  - name: clangd\nmemory:\n  dispositions:\n    skepticism: 0.8\n    literalism: 0.5\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(path)
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"workspace_count":          2,
		"trigger_rule_count":       1,
		"lsp_server_count":         1,
		"disposition_count":        2,
		"disposition_global_count": 2,
	} {
		if got := values[key]; got != want {
			t.Errorf("%s=%#v, want %#v", key, got, want)
		}
	}
	paths := values["workspaces"].([]any)
	providers := values["workspace_providers"].([]any)
	if paths[1] != "/two" || providers[1] != "git" {
		t.Fatalf("workspace projection paths=%#v providers=%#v", paths, providers)
	}
	rows := values["dispositions"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["name"] != "literalism" {
		t.Fatalf("disposition projection=%#v", rows)
	}
}

func TestWorkspaceMutationProjectionPreservesParallelMetadata(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	change := WorkspaceMutation{
		Path: "/client/repo", Provider: "mirror", Remote: "ssh://example.invalid/repo",
		Head: "0123456789abcdef",
	}
	if err := store.WorkspaceAdd(change); err != nil {
		t.Fatal(err)
	}
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"workspaces": "/client/repo", "workspace_providers": "mirror",
		"workspace_vcs_remote": "ssh://example.invalid/repo", "workspace_vcs_head": "0123456789abcdef",
	} {
		items, ok := values[key].([]any)
		if !ok || len(items) != 1 || items[0] != want {
			t.Errorf("%s=%#v, want [%q]", key, values[key], want)
		}
	}
}

func TestWorkingProfileInjectionProjectsFieldsAndCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	document := "identity:\n  working_profile_injection:\n    enabled: true\n    fields:\n      - verbosity\n      - communication_style\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := values["identity_working_profile_injection_fields_count"]; got != 2 {
		t.Fatalf("field count=%v, want 2", got)
	}
	fields, ok := values["identity_working_profile_injection_fields"].([]any)
	if !ok || len(fields) != 2 || fields[0] != "verbosity" || fields[1] != "communication_style" {
		t.Fatalf("fields=%#v", values["identity_working_profile_injection_fields"])
	}
}

func TestProfileConfigLifecycle(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(filepath.Join(root, "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if present, err := store.ProfilePresent("coder"); err != nil || present {
		t.Fatalf("initial presence=(%v, %v), want (false, nil)", present, err)
	}
	if err := store.ProfileCreate("coder"); err != nil {
		t.Fatal(err)
	}
	if present, err := store.ProfilePresent("coder"); err != nil || !present {
		t.Fatalf("presence=(%v, %v), want (true, nil)", present, err)
	}
	profiles, err := store.ProfileList()
	if err != nil || len(profiles) != 1 || profiles[0] != "coder" {
		t.Fatalf("profiles=%v, %v", profiles, err)
	}
	body, err := os.ReadFile(filepath.Join(root, "profiles", "coder", "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Created by the Aimee config module.\nprovider: claude\nguardrail_mode: approve\n" {
		t.Fatalf("unexpected profile config %q", body)
	}
	if err := store.ProfileCreate("../escape"); err == nil {
		t.Fatal("path traversal profile name accepted")
	}
	if err := store.ProfileDelete("coder"); err != nil {
		t.Fatal(err)
	}
	if present, err := store.ProfilePresent("coder"); err != nil || present {
		t.Fatalf("post-delete presence=(%v, %v), want (false, nil)", present, err)
	}
	if err := store.ProfileDelete("coder"); err == nil {
		t.Fatal("deleting a missing profile succeeded")
	}
}

func TestProfileLifecycleUsesGlobalRootAndProtectsActiveProfile(t *testing.T) {
	root := t.TempDir()
	defaultStore, err := NewStore(filepath.Join(root, "aimee.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := defaultStore.ProfileCreate("active"); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(root, "profiles", "active", "aimee.yaml")
	activeStore, err := NewStore(activePath)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := activeStore.ProfileList()
	if err != nil || len(profiles) != 1 || profiles[0] != "active" {
		t.Fatalf("profiles=%v, %v", profiles, err)
	}
	if err := activeStore.ProfileDelete("active"); err == nil {
		t.Fatal("active profile deletion succeeded")
	}
	if present, err := activeStore.ProfilePresent("active"); err != nil || !present {
		t.Fatalf("active profile was damaged: present=%v err=%v", present, err)
	}
}

func TestPolicyWritesPreserveUnrelatedConfigAndAreImmediatelyLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("provider: codex\ncustom:\n  keep: yes\nautonomy:\n  concurrency: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Int("autonomy.concurrency", 8); got != 2 {
		t.Fatalf("concurrency=%d", got)
	}
	if err := store.Set("autonomy.concurrency", float64(5)); err != nil {
		t.Fatal(err)
	}
	if got := store.Int("autonomy.concurrency", 8); got != 5 {
		t.Fatalf("live concurrency=%d", got)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "provider: codex") || !strings.Contains(string(content), "keep: yes") {
		t.Fatalf("unrelated config lost:\n%s", content)
	}
	if err := store.Set("autonomy.max_turns", -1); err == nil {
		t.Fatal("negative policy accepted")
	}
}

func TestPolicyProjectionIncludesRuntimeConcurrencyDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	values, err := store.Values()
	if err != nil {
		t.Fatal(err)
	}
	if got := values["autonomy.concurrency"]; got != 5 {
		t.Fatalf("autonomy.concurrency default=%v, want 5", got)
	}
	if got := values["autonomy.per_workflow_concurrency"]; got != 1 {
		t.Fatalf("autonomy.per_workflow_concurrency default=%v, want 1", got)
	}
	if got := values["autonomy.delegate_pending_secs"]; got != 120 {
		t.Fatalf("autonomy.delegate_pending_secs default=%v, want 120", got)
	}
	if err := store.Set("autonomy.delegate_pending_secs", 1); err == nil {
		t.Fatal("delegate lease below minimum accepted")
	}
	if err := store.Set("autonomy.delegate_pending_secs", 3601); err == nil {
		t.Fatal("delegate lease above maximum accepted")
	}
}

func TestEditableProjectionAndStructuralConflictsFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("provider_token: secret\nautonomy: broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(path)
	values, err := store.Values()
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := values["provider_token"]; leaked {
		t.Fatal("secret leaked through editable config projection")
	}
	if err := store.Set("security.token", "replacement"); err == nil {
		t.Fatal("unknown config key accepted")
	}
	if err := store.Set("autonomy.concurrency", 3); err == nil {
		t.Fatal("scalar parent was destructively replaced")
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "autonomy: broken") {
		t.Fatalf("config mutated:\n%s", content)
	}
}

func TestEffectiveSnapshotNeverExposesCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	content := "db2_url: postgres://plaintext\nembedder_api_key: leaked\nkb:\n  curator:\n    provider_api_key: nested\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(path)
	values, _, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"db2_url", "embedder_api_key", "kb_curator_provider_api_key"} {
		if _, exists := values[key]; exists {
			t.Fatalf("credential %q leaked in snapshot", key)
		}
		if err := store.Set(key, "replacement"); err == nil {
			t.Fatalf("credential %q accepted by config mutation", key)
		}
	}
}

func TestSnapshotVersionAndDynamicDB1Path(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, _ := NewStore(path)
	values, first, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := values["db1_path"]; got != filepath.Join(filepath.Dir(path), "aimee.db") {
		t.Fatalf("db1_path=%v", got)
	}
	if len(first) != 64 {
		t.Fatalf("document version=%q", first)
	}
	if err := store.Set("autonomy.max_turns", 333); err != nil {
		t.Fatal(err)
	}
	second, err := store.Version("")
	if err != nil || second == first {
		t.Fatalf("version after write=%q, err=%v", second, err)
	}
}

func TestAtomicMutationsRemainConsistentUnderConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("typed_facts_enabled: false\nkeep-me: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(path)
	enabled, promote := true, false
	threshold := 7
	if err := store.SetTypedFacts(TypedFactsMutation{Enabled: &enabled, AutoPromote: &promote,
		PromoteThreshold: &threshold}); err != nil {
		t.Fatal(err)
	}
	values, _, _ := store.Snapshot()
	if values["typed_facts_enabled"] != true || values["kb_typed_facts_auto_promote_enabled"] != false ||
		values["kb_typed_facts_promote_threshold"] != 7 {
		t.Fatalf("typed-fact mutation was not atomic: %#v", values)
	}
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(document)
	if !strings.Contains(text, "kb:\n") || !strings.Contains(text, "  typed_facts:\n") ||
		!strings.Contains(text, "    promote_threshold: 7") || strings.Contains(text, "typed_facts_enabled:") ||
		!strings.Contains(text, "keep-me: true") {
		t.Fatalf("typed-fact document is not canonical:\n%s", text)
	}
	promote = true
	if err := store.SetTypedFacts(TypedFactsMutation{AutoPromote: &promote}); err != nil {
		t.Fatal(err)
	}
	values, _, _ = store.Snapshot()
	if values["typed_facts_enabled"] != true || values["kb_typed_facts_auto_promote_enabled"] != true ||
		values["kb_typed_facts_promote_threshold"] != 7 {
		t.Fatalf("partial typed-fact mutation did not preserve siblings: %#v", values)
	}

	var group sync.WaitGroup
	for i := 1; i <= 16; i++ {
		group.Add(1)
		go func(limit int) {
			defer group.Done()
			if err := store.SetModelConcurrency(ModelConcurrencyMutation{Model: "shared", Limit: limit}); err != nil {
				t.Errorf("set model concurrency: %v", err)
			}
		}(i)
	}
	group.Wait()
	values, _, _ = store.Snapshot()
	entries, ok := values["concurrency_per_model"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("concurrent registry=%#v", values["concurrency_per_model"])
	}
}

func TestTriggerRulesUseOptimisticVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, _ := NewStore(path)
	version, err := store.Version("trigger_rules")
	if err != nil {
		t.Fatal(err)
	}
	rules := []map[string]any{{"source": "watch-dir", "pipeline": map[string]any{"template": "build", "workspace": "/repo"}}}
	if err := store.SetVersioned("trigger_rules", rules, version); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVersioned("trigger_rules", []any{}, version); err == nil {
		t.Fatal("stale trigger edit accepted")
	}
}

func TestTriggerRulesRejectOversizedRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, _ := NewStore(path)
	version, err := store.Version("trigger_rules")
	if err != nil {
		t.Fatal(err)
	}
	rules := make([]map[string]any, MaxTriggerRules+1)
	for i := range rules {
		rules[i] = map[string]any{
			"source":   "watch-dir",
			"pipeline": map[string]any{"template": "build", "workspace": "/repo"},
		}
	}
	if err := store.SetVersioned("trigger_rules", rules, version); err == nil || !strings.Contains(err.Error(), "maximum is 32") {
		t.Fatalf("oversized registry error = %v", err)
	}
}

func TestTriggerRulesRejectUnsafeHumanInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, _ := NewStore(path)
	version, _ := store.Version("trigger_rules")
	base := map[string]any{
		"source": "watch-dir", "event": "docs/proposals/pending",
		"pipeline": map[string]any{"template": "build", "workspace": "/repo"},
	}
	for name, mutate := range map[string]func(map[string]any){
		"traversal":          func(rule map[string]any) { rule["event"] = "../outside" },
		"relative-workspace": func(rule map[string]any) { rule["pipeline"].(map[string]any)["workspace"] = "repo" },
		"git-option":         func(rule map[string]any) { rule["schedule"] = "--all" },
		"negative-cap":       func(rule map[string]any) { rule["pipeline"].(map[string]any)["max_spend_usd"] = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			rule := map[string]any{
				"source": base["source"], "event": base["event"],
				"pipeline": map[string]any{"template": "build", "workspace": "/repo"},
			}
			mutate(rule)
			if err := store.SetVersioned("trigger_rules", []map[string]any{rule}, version); err == nil {
				t.Fatal("unsafe trigger rule was accepted")
			}
		})
	}
}

// A wall cap below the write-role floor makes every implement stage refuse
// before it starts, so the stage can never finish however often it retries.
// Rejecting the value when it is set reports that as the configuration error it
// is, instead of leaving it to be inferred from dying attempts.
func TestWallCapBelowWriteRoleFloorIsRejectedNamingBothValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("provider: codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Set("autonomy.max_wall_secs", float64(MinAutonomyMaxWallSecs-1))
	if err == nil {
		t.Fatal("a wall cap under which no write stage can ever finish was accepted")
	}
	if !strings.Contains(err.Error(), "359") || !strings.Contains(err.Error(), "360") {
		t.Fatalf("error %q must name both the configured value and the required floor", err)
	}
	// And where the floor comes from, so it reads as derived rather than chosen.
	if !strings.Contains(err.Error(), "300s verifier reserve") ||
		!strings.Contains(err.Error(), "60s minimum run") {
		t.Fatalf("error %q must name the two components the floor is derived from", err)
	}
}

// The shipped default must keep loading. A floor that rejected the default would
// be a worse failure than the misconfiguration it exists to catch.
func TestDefaultWallCapRemainsAcceptable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("provider: codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	shipped, ok := number(policyDefaults["autonomy.max_wall_secs"])
	if !ok {
		t.Fatalf("shipped default missing: %#v", policyDefaults["autonomy.max_wall_secs"])
	}
	if shipped < MinAutonomyMaxWallSecs {
		t.Fatalf("shipped default %d is below the enforced floor %d", shipped, MinAutonomyMaxWallSecs)
	}
	if err := store.Set("autonomy.max_wall_secs", float64(shipped)); err != nil {
		t.Fatalf("shipped default rejected: %v", err)
	}
	if err := store.Set("autonomy.max_wall_secs", float64(MinAutonomyMaxWallSecs)); err != nil {
		t.Fatalf("exact floor rejected: %v", err)
	}
}

func TestMemoryRecallLaneDefaultsAndYAMLOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	assertValues := func(want map[string]any) {
		t.Helper()
		values, err := store.EffectiveValues()
		if err != nil {
			t.Fatal(err)
		}
		for key, expected := range want {
			if got := values[key]; got != expected {
				t.Errorf("%s = %#v, want %#v", key, got, expected)
			}
		}
	}

	assertValues(map[string]any{
		"memory_recall_lanes_enabled":       json.Number("0"),
		"memory_recall_lanes_summary_kinds": "",
		"memory_recall_lanes_fact_kinds":    "",
		"memory_recall_lanes_k_summary":     json.Number("0"),
		"memory_recall_lanes_k_fact":        json.Number("0"),
		"memory_recall_lanes_floor_summary": json.Number("4"),
		"memory_recall_lanes_floor_fact":    json.Number("4"),
	})

	if err := os.WriteFile(path, []byte("memory_recall_lanes:\n"+
		"  enabled: true\n"+
		"  summary_kinds: episode\n"+
		"  fact_kinds: fact,preference\n"+
		"  k_summary: 30\n"+
		"  k_fact: 25\n"+
		"  floor_summary: 6\n"+
		"  floor_fact: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertValues(map[string]any{
		"memory_recall_lanes_enabled":       true,
		"memory_recall_lanes_summary_kinds": "episode",
		"memory_recall_lanes_fact_kinds":    "fact,preference",
		"memory_recall_lanes_k_summary":     30,
		"memory_recall_lanes_k_fact":        25,
		"memory_recall_lanes_floor_summary": 6,
		"memory_recall_lanes_floor_fact":    3,
	})

	if err := os.WriteFile(path, []byte("memory_recall_lanes:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertValues(map[string]any{
		"memory_recall_lanes_enabled":       false,
		"memory_recall_lanes_floor_summary": json.Number("4"),
		"memory_recall_lanes_floor_fact":    json.Number("4"),
	})

	if err := os.WriteFile(path, []byte("memory_recall_lanes:\n  enabled: true\n  floor_summary: 0\n  floor_fact: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertValues(map[string]any{
		"memory_recall_lanes_enabled":       true,
		"memory_recall_lanes_floor_summary": 0,
		"memory_recall_lanes_floor_fact":    0,
	})
}

func TestMalformedReloadKeepsLastGoodSnapshotAndRejectsMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aimee.yaml")
	if err := os.WriteFile(path, []byte("feature_auto_promote: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Value("feature_auto_promote")
	if err != nil || value != true {
		t.Fatalf("seed snapshot: value=%#v err=%v", value, err)
	}

	malformed := []byte("invalid: [unterminated\n")
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err = store.Value("feature_auto_promote")
	if err != nil || value != true {
		t.Fatalf("last-good snapshot: value=%#v err=%v", value, err)
	}
	if err := store.Set("feature_auto_promote", false); err == nil {
		t.Fatal("mutation unexpectedly overwrote malformed configuration")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(malformed) {
		t.Fatalf("malformed file changed: %q", content)
	}

	if err := os.WriteFile(path, []byte("feature_auto_promote: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err = store.Value("feature_auto_promote")
	if err != nil || value != false {
		t.Fatalf("recovered snapshot: value=%#v err=%v", value, err)
	}
}
