// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package score

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/config"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/constants"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/node"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/scheduler/selctx"
)

func TestExternalHTTPScoreSelectUsesSidecarScores(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %s, want application/json", got)
		}
		var req externalHTTPScoreRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Mode != "prefer-node-b" {
			t.Fatalf("mode = %s, want prefer-node-b", req.Mode)
		}
		if req.TemplateID != "tpl-test" {
			t.Fatalf("template_id = %s, want tpl-test", req.TemplateID)
		}
		if len(req.Nodes) != 2 {
			t.Fatalf("nodes = %d, want 2", len(req.Nodes))
		}
		if req.InstanceType != "cubebox" {
			t.Fatalf("instance_type = %s, want cubebox", req.InstanceType)
		}
		if req.Nodes[0].NodeID != "node-a" ||
			req.Nodes[0].NodeIP != "10.0.0.1" ||
			req.Nodes[0].InstanceType != "cubebox" ||
			req.Nodes[0].MvmNum != 10 ||
			req.Nodes[0].RealTimeCreateNum != 1 ||
			req.Nodes[0].LocalCreateNum != 1 ||
			req.Nodes[0].CreateConcurrentNum != 30 ||
			req.Nodes[0].QuotaCPU != 64000 ||
			req.Nodes[0].QuotaMem != 131072 ||
			req.Nodes[0].QuotaCPUUsage != 20000 ||
			req.Nodes[0].QuotaMemUsage != 32768 ||
			req.Nodes[0].CPUUtil != 30 ||
			req.Nodes[0].MemUsage != 65536 {
			t.Fatalf("first request node = %+v, want node-a metrics", req.Nodes[0])
		}
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 10,
				"node-b": 90,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	scorer := NewExternalHTTPScore()
	got, err := scorer.Select(externalHTTPScoreTestCtx())
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got.Len() != 2 {
		t.Fatalf("len(scores) = %d, want 2", got.Len())
	}
	if got[0].ID() != "node-a" || got[0].Score != 10 {
		t.Fatalf("got first score %+v, want node-a=10", got[0])
	}
	if got[1].ID() != "node-b" || got[1].Score != 90 {
		t.Fatalf("got second score %+v, want node-b=90", got[1])
	}

	sorted := got.AllSortByScore()
	if sorted[0].ID() != "node-b" {
		t.Fatalf("highest score node = %s, want node-b", sorted[0].ID())
	}
}

func TestExternalHTTPScoreRequestBodyMatchesProtocol(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	const wantMode = "protocol-contract"
	var (
		gotMethod      string
		gotContentType string
		gotBody        []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotBody = body
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 10,
				"node-b": 90,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfigWithPluginConfig(t, `
        weight: 1
        endpoint: "`+server.URL+`"
        timeout: 1s
        mode: `+wantMode+`
`)

	selCtx := externalHTTPScoreTestCtx()
	got, err := NewExternalHTTPScore().Select(selCtx)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got.Len() != 2 {
		t.Fatalf("len(scores) = %d, want 2", got.Len())
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %s, want application/json", gotContentType)
	}

	var raw map[string]any
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatalf("unmarshal request JSON: %v\nbody=%s", err, gotBody)
	}

	// Source of truth: docs/dev/external-http-score.md and externalHTTPScoreRequest.
	// instance_id is not a protocol field; request_id is reserved but currently omitted.
	for _, key := range []string{"mode", "instance_type", "template_id", "nodes"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("request JSON missing %q", key)
		}
	}
	if _, ok := raw["instance_id"]; ok {
		t.Errorf("request JSON has instance_id=%v; protocol has no such field", raw["instance_id"])
	}
	if _, ok := raw["request_id"]; ok {
		t.Errorf("request_id present = %v, want omitted (SelectorCtx does not populate it)", raw["request_id"])
	}

	if raw["mode"] != wantMode {
		t.Fatalf("mode = %v, want plugin_conf.external_http_score.mode %q", raw["mode"], wantMode)
	}
	if raw["instance_type"] != selCtx.InstanceType {
		t.Fatalf("instance_type = %v, want %q", raw["instance_type"], selCtx.InstanceType)
	}
	if raw["template_id"] != selCtx.ReqRes.TemplateID {
		t.Fatalf("template_id = %v, want %q", raw["template_id"], selCtx.ReqRes.TemplateID)
	}

	nodes, ok := raw["nodes"].([]any)
	if !ok {
		t.Fatalf("nodes type = %T, want JSON array", raw["nodes"])
	}
	candidates := selCtx.Nodes()
	if len(nodes) != candidates.Len() {
		t.Fatalf("len(nodes) = %d, want %d", len(nodes), candidates.Len())
	}

	nodeKeys := []string{
		"node_id",
		"node_ip",
		"cpu_util",
		"mem_usage",
		"quota_mem",
		"real_time_create_num",
		"create_concurrent_num",
	}
	for i, item := range nodes {
		n, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("nodes[%d] type = %T, want object", i, item)
		}
		for _, key := range nodeKeys {
			if _, ok := n[key]; !ok {
				t.Errorf("nodes[%d] missing %q", i, key)
			}
		}
		if _, ok := n["instance_id"]; ok {
			t.Errorf("nodes[%d] has instance_id=%v; node protocol has no such field", i, n["instance_id"])
		}

		cand := candidates[i]
		if n["node_id"] != cand.ID() {
			t.Errorf("nodes[%d].node_id = %v, want %s", i, n["node_id"], cand.ID())
		}
		if n["node_ip"] != cand.IP {
			t.Errorf("nodes[%d].node_ip = %v, want %s", i, n["node_ip"], cand.IP)
		}
		if !jsonNumberEquals(n["cpu_util"], cand.CpuUtil) {
			t.Errorf("nodes[%d].cpu_util = %v, want %v", i, n["cpu_util"], cand.CpuUtil)
		}
		if !jsonNumberEquals(n["mem_usage"], float64(cand.MemUsage)) {
			t.Errorf("nodes[%d].mem_usage = %v, want %d", i, n["mem_usage"], cand.MemUsage)
		}
		if !jsonNumberEquals(n["quota_mem"], float64(cand.QuotaMem)) {
			t.Errorf("nodes[%d].quota_mem = %v, want %d", i, n["quota_mem"], cand.QuotaMem)
		}
		if !jsonNumberEquals(n["real_time_create_num"], float64(cand.RealTimeCreateNum)) {
			t.Errorf("nodes[%d].real_time_create_num = %v, want %d", i, n["real_time_create_num"], cand.RealTimeCreateNum)
		}
		if !jsonNumberEquals(n["create_concurrent_num"], float64(cand.CreateConcurrentNum)) {
			t.Errorf("nodes[%d].create_concurrent_num = %v, want %d", i, n["create_concurrent_num"], cand.CreateConcurrentNum)
		}
	}
}

func jsonNumberEquals(got any, want float64) bool {
	n, ok := got.(float64)
	return ok && n == want
}

func TestExternalHTTPScoreRejectsInvalidScore(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 101,
				"node-b": 90,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want invalid score error")
	}
}

func TestExternalHTTPScoreValidateResponseBoundaries(t *testing.T) {
	knownNodes := map[string]struct{}{
		"node-a": {},
		"node-b": {},
	}

	tests := []struct {
		name    string
		scores  map[string]float64
		wantErr bool
	}{
		{
			name: "accepts zero and one hundred",
			scores: map[string]float64{
				"node-a": 0,
				"node-b": 100,
			},
		},
		{
			name: "rejects negative score",
			scores: map[string]float64{
				"node-a": -1,
				"node-b": 100,
			},
			wantErr: true,
		},
		{
			name: "rejects NaN score",
			scores: map[string]float64{
				"node-a": math.NaN(),
				"node-b": 100,
			},
			wantErr: true,
		},
		{
			name: "rejects positive infinity score",
			scores: map[string]float64{
				"node-a": math.Inf(1),
				"node-b": 100,
			},
			wantErr: true,
		},
		{
			name: "rejects negative infinity score",
			scores: map[string]float64{
				"node-a": math.Inf(-1),
				"node-b": 100,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExternalHTTPScoreResponse(tt.scores, knownNodes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateExternalHTTPScoreResponse() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExternalHTTPScoreValidateCandidates(t *testing.T) {
	tests := []struct {
		name    string
		nodes   node.NodeList
		wantErr bool
	}{
		{
			name: "accepts unique node IDs",
			nodes: node.NodeList{
				{InsID: "node-a"},
				{InsID: "node-b"},
			},
		},
		{
			name: "accepts IP fallback ID",
			nodes: node.NodeList{
				{IP: "10.0.0.1"},
				{IP: "10.0.0.2"},
			},
		},
		{
			name: "rejects nil node",
			nodes: node.NodeList{
				{InsID: "node-a"},
				nil,
			},
			wantErr: true,
		},
		{
			name: "rejects empty node ID",
			nodes: node.NodeList{
				{InsID: "node-a"},
				{},
			},
			wantErr: true,
		},
		{
			name: "rejects duplicate node ID",
			nodes: node.NodeList{
				{InsID: "node-a"},
				{InsID: "node-a"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateExternalHTTPScoreCandidates(tt.nodes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateExternalHTTPScoreCandidates() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExternalHTTPScoreRejectsDuplicateCandidateBeforeRequest(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("external HTTP scorer should reject duplicate candidates before calling endpoint")
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	selCtx := externalHTTPScoreTestCtx()
	selCtx.SetNodes(node.NodeList{
		{InsID: "node-a"},
		{InsID: "node-a"},
	})

	_, err := NewExternalHTTPScore().Select(selCtx)
	if err == nil {
		t.Fatal("Select() error = nil, want duplicate candidate error")
	}
}

func TestExternalHTTPScoreRejectsHTTPError(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want HTTP status error")
	}
}

func TestExternalHTTPScoreRejectsUnknownNode(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a":  10,
				"unknown": 90,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want unknown node error")
	}
}

func TestExternalHTTPScoreRejectsMissingNode(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 10,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want missing node error")
	}
}

func TestExternalHTTPScoreRejectsEmptyScores(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want empty scores error")
	}
}

func TestExternalHTTPScoreRejectsMissingScoresField(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want missing scores error")
	}
}

func TestExternalHTTPScoreRejectsMalformedJSON(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{"))
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want malformed JSON error")
	}
}

func TestExternalHTTPScoreTimesOut(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 10,
				"node-b": 90,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfigWithPluginConfig(t, `
        weight: 1
        endpoint: "`+server.URL+`"
        timeout: 10ms
        mode: prefer-node-b
`)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want timeout error")
	}
}

func TestExternalHTTPScoreUsesDefaultTimeout(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 10,
				"node-b": 90,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfigWithPluginConfig(t, `
        weight: 1
        endpoint: "`+server.URL+`"
        mode: prefer-node-b
`)

	start := time.Now()
	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want default timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Select() took %s, want default timeout to fire quickly", elapsed)
	}
}

func TestExternalHTTPScoreSkipsWhenEndpointEmpty(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	initExternalHTTPScoreTestConfig(t, "")

	got, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err != nil {
		t.Fatalf("Select() error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("Select() = %+v, want nil scores", got)
	}
}

func TestExternalHTTPScoreSkipsWhenDisabled(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("disabled external HTTP scorer should not call endpoint")
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfigWithPluginConfig(t, `
        weight: 1
        endpoint: "`+server.URL+`"
        timeout: 1s
        mode: prefer-node-b
        disable: true
`)

	got, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err != nil {
		t.Fatalf("Select() error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("Select() = %+v, want nil scores", got)
	}
}

func TestExternalHTTPScoreZeroWeightSkipsRequest(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
		t.Error("zero-weight external HTTP scorer should not call endpoint")
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfigWithPluginConfig(t, `
        weight: 0
        endpoint: "`+server.URL+`"
        timeout: 1s
`)

	scorer := NewExternalHTTPScore()
	if !scorer.Disable() {
		t.Fatal("Disable() = false, want true for weight: 0")
	}
	got, err := scorer.Select(externalHTTPScoreTestCtx())
	if err != nil {
		t.Fatalf("Select() error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("Select() = %+v, want nil", got)
	}
	if requested {
		t.Fatal("zero-weight external HTTP scorer sent a request")
	}
}

func TestExternalHTTPScoreRejectsOversizedResponseBody(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, externalHTTPScoreMaxResponseBytes+1))
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	_, err := NewExternalHTTPScore().Select(externalHTTPScoreTestCtx())
	if err == nil {
		t.Fatal("Select() error = nil, want oversized response error")
	}
}

func TestExternalHTTPScoreRegisteredByConfig(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 10,
				"node-b": 90,
			},
		})
	}))
	defer server.Close()

	initExternalHTTPScoreTestConfig(t, server.URL)

	selectors := NewSelector(context.Background())
	if len(selectors) != 1 {
		t.Fatalf("len(selectors) = %d, want 1", len(selectors))
	}
	if selectors[0].ID() != constants.SelectorScoreID+"/"+externalHTTPScoreName {
		t.Fatalf("selector ID = %s, want external_http_score", selectors[0].ID())
	}
}

func TestExternalHTTPScoreUnknownScorerDoesNotBlockKnownScorer(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(externalHTTPScoreResponse{
			Scores: map[string]float64{
				"node-a": 10,
				"node-b": 90,
			},
		})
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "cubemaster.yaml")
	content := `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - unknown_score
      - external_http_score
    resource_weights:
      external_http_score: 1
    plugin_conf:
      external_http_score:
        weight: 1
        endpoint: "` + server.URL + `"
        timeout: 1s
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	if _, err := config.Init(); err != nil {
		t.Fatalf("config.Init(): %v", err)
	}

	selectors := NewSelector(context.Background())
	if len(selectors) != 1 {
		t.Fatalf("len(selectors) = %d, want 1", len(selectors))
	}
	if selectors[0].ID() != constants.SelectorScoreID+"/"+externalHTTPScoreName {
		t.Fatalf("selector ID = %s, want external_http_score", selectors[0].ID())
	}
}

func TestExternalHTTPScoreMissingPluginConfigFailsFast(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	configPath := filepath.Join(t.TempDir(), "cubemaster.yaml")
	content := `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - external_http_score
    resource_weights:
      external_http_score: 1
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	if _, err := config.Init(); err == nil {
		t.Fatal("config.Init() error = nil, want missing plugin_conf error")
	} else if !strings.Contains(err.Error(), "plugin_conf.external_http_score") {
		t.Fatalf("config.Init() error = %v, want plugin_conf.external_http_score", err)
	}
}

func TestExternalHTTPScoreNotEnabledByDefault(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("external HTTP scorer should not be called unless it is enabled")
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "cubemaster.yaml")
	content := `common: {}
log: {}
scheduler:
  score:
    resource_weights:
      external_http_score: 1
    plugin_conf:
      external_http_score:
        weight: 1
        endpoint: "` + server.URL + `"
        timeout: 1s
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	if _, err := config.Init(); err != nil {
		t.Fatalf("config.Init(): %v", err)
	}

	selectors := NewSelector(context.Background())
	if len(selectors) != 0 {
		t.Fatalf("len(selectors) = %d, want 0", len(selectors))
	}
}

func initExternalHTTPScoreTestConfig(t *testing.T, endpoint string) {
	t.Helper()

	initExternalHTTPScoreTestConfigWithPluginConfig(t, `
        weight: 1
        endpoint: "`+endpoint+`"
        timeout: 1s
        mode: prefer-node-b
`)
}

func initExternalHTTPScoreTestConfigWithPluginConfig(t *testing.T, pluginConfig string) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "cubemaster.yaml")
	content := `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - external_http_score
    resource_weights:
      external_http_score: 1
    plugin_conf:
      external_http_score:` + pluginConfig
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	if _, err := config.Init(); err != nil {
		t.Fatalf("config.Init(): %v", err)
	}
}

func externalHTTPScoreTestCtx() *selctx.SelectorCtx {
	ctx := selctx.New("random")
	ctx.Ctx = context.Background()
	ctx.InstanceType = "cubebox"
	ctx.ReqRes = &selctx.RequestResource{TemplateID: "tpl-test"}
	ctx.SetNodes(node.NodeList{
		{
			InsID:               "node-a",
			IP:                  "10.0.0.1",
			InstanceType:        "cubebox",
			MvmNum:              10,
			RealTimeCreateNum:   1,
			LocalCreateNum:      1,
			CreateConcurrentNum: 30,
			QuotaCpu:            64000,
			QuotaMem:            131072,
			QuotaCpuUsage:       20000,
			QuotaMemUsage:       32768,
			CpuUtil:             30,
			MemUsage:            65536,
		},
		{
			InsID:               "node-b",
			IP:                  "10.0.0.2",
			InstanceType:        "cubebox",
			MvmNum:              2,
			RealTimeCreateNum:   0,
			LocalCreateNum:      0,
			CreateConcurrentNum: 30,
			QuotaCpu:            64000,
			QuotaMem:            131072,
			QuotaCpuUsage:       10000,
			QuotaMemUsage:       16384,
			CpuUtil:             15,
			MemUsage:            32768,
		},
	})
	return ctx
}
