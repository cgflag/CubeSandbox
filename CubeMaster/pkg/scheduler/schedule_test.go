// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/config"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/node"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/scheduler/selctx"
	sfilter "github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/selector/filter"
	sscore "github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/selector/score"
)

func TestShouldSkipBackoffForTemplate(t *testing.T) {
	origFilters := scheduler.filter
	defer func() {
		scheduler.filter = origFilters
	}()

	tests := []struct {
		name    string
		ctx     *selctx.SelectorCtx
		filters []sfilter.Selector
		want    bool
	}{
		{
			name: "nil selector context",
			ctx:  nil,
			filters: []sfilter.Selector{
				sfilter.NewTemplateLocalityFilter(),
			},
			want: false,
		},
		{
			name: "request without template",
			ctx: &selctx.SelectorCtx{
				ReqRes: &selctx.RequestResource{},
			},
			filters: []sfilter.Selector{
				sfilter.NewTemplateLocalityFilter(),
			},
			want: false,
		},
		{
			name: "request with template but filter disabled",
			ctx: &selctx.SelectorCtx{
				ReqRes: &selctx.RequestResource{TemplateID: "tpl-1"},
			},
			filters: nil,
			want:    false,
		},
		{
			name: "request with template and filter enabled",
			ctx: &selctx.SelectorCtx{
				ReqRes: &selctx.RequestResource{TemplateID: "tpl-1"},
			},
			filters: []sfilter.Selector{
				sfilter.NewTemplateLocalityFilter(),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler.filter = tt.filters
			if got := shouldSkipBackoffForTemplate(tt.ctx); got != tt.want {
				t.Fatalf("shouldSkipBackoffForTemplate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunScoreFilterSkipsFailedScorers(t *testing.T) {
	origPostScore := scheduler.postScore
	defer func() {
		scheduler.postScore = origPostScore
	}()
	scheduler.postScore = nil

	nodeA := &node.Node{InsID: "node-a", MvmNum: 1}
	nodeB := &node.Node{InsID: "node-b", MvmNum: 2}
	selCtx := selctx.New("random")
	selCtx.Ctx = context.Background()
	selCtx.SetNodes(node.NodeList{nodeA, nodeB})

	err := runScoreFilter(selCtx, []sscore.Selector{
		testScoreSelector{
			err: errors.New("external scorer unavailable"),
		},
		testScoreSelector{
			weight: 2,
			scores: node.NodeScoreList{
				{InsID: "node-a", Score: 10, MvmNum: nodeA.MvmNum, OrigNode: nodeA},
				{InsID: "node-b", Score: 90, MvmNum: nodeB.MvmNum, OrigNode: nodeB},
			},
		},
	})
	if err != nil {
		t.Fatalf("runScoreFilter() error = %v, want nil", err)
	}

	got := selCtx.LeastScoreNodes(-1)
	if got.Len() != 2 {
		t.Fatalf("len(score nodes) = %d, want 2", got.Len())
	}
	if got[0].ID() != "node-b" || got[0].Score != 90 {
		t.Fatalf("highest score = %+v, want node-b=90", got[0])
	}
	if got[1].ID() != "node-a" || got[1].Score != 10 {
		t.Fatalf("lowest score = %+v, want node-a=10", got[1])
	}
}

func TestRunScoreFilterExternalHTTPScoreChangesPlacementOrder(t *testing.T) {
	if runIsolatedSchedulerConfigTest(t) {
		return
	}

	origPostScore := scheduler.postScore
	defer func() {
		scheduler.postScore = origPostScore
	}()
	scheduler.postScore = nil

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"scores":{"node-a":5,"node-b":95}}`))
	}))
	defer server.Close()

	initSchedulerExternalHTTPScoreTestConfig(t, server.URL, "")

	nodeA := &node.Node{InsID: "node-a", MvmNum: 1}
	nodeB := &node.Node{InsID: "node-b", MvmNum: 2}
	selCtx := selctx.New("random")
	selCtx.Ctx = context.Background()
	selCtx.SetNodes(node.NodeList{nodeA, nodeB})

	err := runScoreFilter(selCtx, []sscore.Selector{
		sscore.NewExternalHTTPScore(),
	})
	if err != nil {
		t.Fatalf("runScoreFilter() error = %v, want nil", err)
	}

	got := selCtx.LeastScoreNodes(-1)
	if got.Len() != 2 {
		t.Fatalf("len(score nodes) = %d, want 2", got.Len())
	}
	if got[0].ID() != "node-b" || got[0].Score != 95 {
		t.Fatalf("highest score = %+v, want node-b=95", got[0])
	}
	if got[1].ID() != "node-a" || got[1].Score != 5 {
		t.Fatalf("lowest score = %+v, want node-a=5", got[1])
	}
	if selCtx.Nodes()[0].ID() != "node-b" {
		t.Fatalf("first candidate = %s, want node-b", selCtx.Nodes()[0].ID())
	}
}

func TestRunScoreFilterBinpackScorePrefersFullerNode(t *testing.T) {
	if runIsolatedSchedulerConfigTest(t) {
		return
	}

	origPostScore := scheduler.postScore
	defer func() {
		scheduler.postScore = origPostScore
	}()
	scheduler.postScore = nil

	configPath := filepath.Join(t.TempDir(), "cubemaster.yaml")
	content := `common: {}
log: {}
scheduler:
  ignore_redis_allocation: false
  overcommit_ratio:
    cpu_ratio: 1
    mem_ratio: 1
  score:
    enable_scorers:
      - binpack_score
    plugin_conf:
      binpack_score:
        weight: 1
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	if _, err := config.Init(); err != nil {
		t.Fatalf("config.Init(): %v", err)
	}

	empty := &node.Node{
		InsID: "node-empty", QuotaCpu: 1000, QuotaMem: 1000,
		QuotaCpuUsage: 100, QuotaMemUsage: 100, MvmNum: 1, MaxMvmLimit: 10,
	}
	full := &node.Node{
		InsID: "node-full", QuotaCpu: 1000, QuotaMem: 1000,
		QuotaCpuUsage: 800, QuotaMemUsage: 800, MvmNum: 8, MaxMvmLimit: 10,
	}
	selCtx := selctx.New("random")
	selCtx.Ctx = context.Background()
	selCtx.SetNodes(node.NodeList{empty, full})

	err := runScoreFilter(selCtx, []sscore.Selector{
		sscore.NewBinpackScore(),
	})
	if err != nil {
		t.Fatalf("runScoreFilter() error = %v, want nil", err)
	}
	got := selCtx.LeastScoreNodes(-1)
	if got.Len() != 2 {
		t.Fatalf("len(score nodes) = %d, want 2", got.Len())
	}
	if got[0].ID() != "node-full" {
		t.Fatalf("highest score = %s, want node-full", got[0].ID())
	}
	if got[1].ID() != "node-empty" {
		t.Fatalf("lowest score = %s, want node-empty", got[1].ID())
	}
}

// TestRunScoreFilterBuiltinProfileOverlayChangesPlacementOrder is in-process only.
// It is not CubeAPI/Cubelet E2E, not multi-VM, not Prometheus E2E, and not real
// create latency. It proves scheduler.profile overlay reaches score.NewSelector
// and production runScoreFilter, changing candidate order vs empty profile.
func TestRunScoreFilterBuiltinProfileOverlayChangesPlacementOrder(t *testing.T) {
	if runIsolatedSchedulerConfigTest(t) {
		return
	}

	origPostScore := scheduler.postScore
	defer func() {
		scheduler.postScore = origPostScore
	}()
	scheduler.postScore = nil

	empty := &node.Node{
		InsID: "node-empty", QuotaCpu: 1000, QuotaMem: 1000,
		QuotaCpuUsage: 100, QuotaMemUsage: 100, MvmNum: 1, MaxMvmLimit: 10,
	}
	full := &node.Node{
		InsID: "node-full", QuotaCpu: 1000, QuotaMem: 1000,
		QuotaCpuUsage: 800, QuotaMemUsage: 800, MvmNum: 8, MaxMvmLimit: 10,
	}

	initSchedulerYAML(t, `common: {}
log: {}
scheduler:
  filter:
    enable_filters:
      - cpu
      - mem
  score:
    enable_scorers: []
`)
	baseline := sscore.NewSelector(context.Background())
	if len(baseline) != 0 {
		t.Fatalf("empty profile NewSelector len = %d, want 0", len(baseline))
	}
	baselineCtx := selctx.New("random")
	baselineCtx.Ctx = context.Background()
	baselineCtx.SetNodes(node.NodeList{empty, full})
	if err := runScoreFilter(baselineCtx, baseline); err != nil {
		t.Fatalf("baseline runScoreFilter() error = %v, want nil", err)
	}
	if baselineCtx.Nodes()[0].ID() != "node-empty" {
		t.Fatalf("baseline first candidate = %s, want node-empty (input order, no scorers)", baselineCtx.Nodes()[0].ID())
	}

	initSchedulerYAML(t, `common: {}
log: {}
scheduler:
  profile: binpack_utilization
`)
	enabled := sscore.NewSelector(context.Background())
	if len(enabled) != 1 {
		t.Fatalf("binpack_utilization NewSelector len = %d, want 1", len(enabled))
	}
	if enabled[0].ID() != "Score/binpack_score" {
		t.Fatalf("enabled selector ID = %s, want Score/binpack_score", enabled[0].ID())
	}
	enabledCtx := selctx.New("random")
	enabledCtx.Ctx = context.Background()
	enabledCtx.SetNodes(node.NodeList{empty, full})
	if err := runScoreFilter(enabledCtx, enabled); err != nil {
		t.Fatalf("enabled runScoreFilter() error = %v, want nil", err)
	}
	if enabledCtx.Nodes()[0].ID() != "node-full" {
		t.Fatalf("enabled first candidate = %s, want node-full", enabledCtx.Nodes()[0].ID())
	}
}

func initSchedulerYAML(t *testing.T, yamlBody string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "cubemaster.yaml")
	if err := os.WriteFile(configPath, []byte(yamlBody), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	if _, err := config.Init(); err != nil {
		t.Fatalf("config.Init(): %v", err)
	}
}

const isolatedSchedulerConfigTestEnv = "CUBEMASTER_ISOLATED_SCHEDULER_CONFIG_TEST"

// runIsolatedSchedulerConfigTest runs config-mutating tests in a child test
// process because config exposes no setter that can restore its package-global
// pointer, including the original nil state.
func runIsolatedSchedulerConfigTest(t *testing.T) bool {
	t.Helper()
	if os.Getenv(isolatedSchedulerConfigTestEnv) == t.Name() {
		return false
	}

	originalConfig := config.GetConfig()
	t.Cleanup(func() {
		if got := config.GetConfig(); got != originalConfig {
			t.Errorf("global config changed in parent process: got %p, want %p", got, originalConfig)
		}
	})

	cmd := exec.Command(
		os.Args[0],
		"-test.run=^"+regexp.QuoteMeta(t.Name())+"$",
		"-test.count=1",
	)
	cmd.Env = append(os.Environ(), isolatedSchedulerConfigTestEnv+"="+t.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated test process failed: %v\n%s", err, output)
	}
	return true
}

// TestRunScoreFilterExternalHTTPScoreMockMetricsFlipChangesPlacementOrder is an
// in-process/mock-HTTP test only: not real Prometheus and not live multi-node.
// It proves mock metrics input can flip ExternalHTTPScore ordering through
// scheduler runScoreFilter.
func TestRunScoreFilterExternalHTTPScoreMockMetricsFlipChangesPlacementOrder(t *testing.T) {
	if runIsolatedSchedulerConfigTest(t) {
		return
	}

	origPostScore := scheduler.postScore
	defer func() {
		scheduler.postScore = origPostScore
	}()
	scheduler.postScore = nil

	store := &mockMetricsScoreStore{
		scores: map[string]float64{
			"node-a": 20,
			"node-b": 90,
		},
	}
	var (
		mu           sync.Mutex
		requestCount int
		seenModes    []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Mode  string `json:"mode"`
			Nodes []struct {
				NodeID string `json:"node_id"`
			} `json:"nodes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requestCount++
		seenModes = append(seenModes, req.Mode)
		mu.Unlock()

		snapshot := store.snapshot()
		out := make(map[string]float64, len(req.Nodes))
		for _, n := range req.Nodes {
			out[n.NodeID] = snapshot[n.NodeID]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]map[string]float64{"scores": out})
	}))
	defer server.Close()

	initSchedulerExternalHTTPScoreTestConfig(t, server.URL, "mock_metrics")

	nodeA := &node.Node{InsID: "node-a", MvmNum: 1}
	nodeB := &node.Node{InsID: "node-b", MvmNum: 2}

	runOnce := func(t *testing.T, wantFirst string) {
		t.Helper()
		selCtx := selctx.New("random")
		selCtx.Ctx = context.Background()
		// Start with a fixed candidate order so the assertion reflects scoring, not input order.
		selCtx.SetNodes(node.NodeList{nodeA, nodeB})

		err := runScoreFilter(selCtx, []sscore.Selector{
			sscore.NewExternalHTTPScore(),
		})
		if err != nil {
			t.Fatalf("runScoreFilter() error = %v, want nil", err)
		}
		if selCtx.Nodes().Len() == 0 {
			t.Fatalf("candidate nodes empty after score")
		}
		if selCtx.Nodes()[0].ID() != wantFirst {
			t.Fatalf("first candidate = %s, want %s (in-process mock metrics)", selCtx.Nodes()[0].ID(), wantFirst)
		}
	}

	// Initial mock metrics prefer node-b.
	runOnce(t, "node-b")

	// Flip mock metrics so node-a scores higher; same scheduler score path.
	store.replace(map[string]float64{
		"node-a": 95,
		"node-b": 10,
	})
	runOnce(t, "node-a")

	mu.Lock()
	defer mu.Unlock()
	if requestCount < 2 {
		t.Fatalf("scorer request count = %d, want >= 2", requestCount)
	}
	for i, mode := range seenModes {
		if mode != "mock_metrics" {
			t.Fatalf("request[%d] mode = %q, want mock_metrics", i, mode)
		}
	}
}

type mockMetricsScoreStore struct {
	mu     sync.RWMutex
	scores map[string]float64
}

func (s *mockMetricsScoreStore) snapshot() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]float64, len(s.scores))
	for id, score := range s.scores {
		out[id] = score
	}
	return out
}

func (s *mockMetricsScoreStore) replace(scores map[string]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scores = make(map[string]float64, len(scores))
	for id, score := range scores {
		s.scores[id] = score
	}
}

func initSchedulerExternalHTTPScoreTestConfig(t *testing.T, endpoint, mode string) {
	t.Helper()

	modeLine := ""
	if mode != "" {
		modeLine = "\n        mode: \"" + mode + "\""
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
    plugin_conf:
      external_http_score:
        weight: 1
        endpoint: "` + endpoint + `"
        timeout: 1s` + modeLine + `
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	if _, err := config.Init(); err != nil {
		t.Fatalf("config.Init(): %v", err)
	}
}

type testScoreSelector struct {
	weight  float64
	disable bool
	scores  node.NodeScoreList
	err     error
}

func (s testScoreSelector) Select(*selctx.SelectorCtx) (node.NodeScoreList, error) {
	return s.scores, s.err
}

func (s testScoreSelector) ID() string {
	return "test_score"
}

func (s testScoreSelector) Weight() float64 {
	return s.weight
}

func (s testScoreSelector) Disable() bool {
	return s.disable
}
