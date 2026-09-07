// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package score

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/config"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/constants"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/node"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/scheduler/selctx"
)

func TestBinpackScoreMissingPluginConfigUsesDefaults(t *testing.T) {
	initBinpackScoreTestConfig(t, `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - binpack_score
    resource_weights:
      binpack_score: 1
`)

	scorer := NewBinpackScore()
	if scorer.Disable() {
		t.Fatal("missing plugin_conf.binpack_score should keep binpack_score enabled")
	}
	if scorer.Weight() != 1 {
		t.Fatalf("Weight() = %v, want 1", scorer.Weight())
	}
	if scorer.ID() != constants.SelectorScoreID+"/"+binpackScoreName {
		t.Fatalf("ID() = %s, want binpack_score", scorer.ID())
	}
}

func TestBinpackScoreDisableSkipsSelect(t *testing.T) {
	initBinpackScoreTestConfig(t, `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - binpack_score
    plugin_conf:
      binpack_score:
        disable: true
`)

	scorer := NewBinpackScore()
	if !scorer.Disable() {
		t.Fatal("Disable() = false, want true")
	}
	got, err := scorer.Select(binpackScoreTestCtx(t))
	if err != nil {
		t.Fatalf("Select() error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("Select() = %+v, want nil", got)
	}
}

func TestBinpackScorePrefersFullerNode(t *testing.T) {
	initBinpackScoreTestConfig(t, `common: {}
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
`)

	scorer := NewBinpackScore()
	got, err := scorer.Select(binpackScoreTestCtx(t))
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got.Len() != 2 {
		t.Fatalf("len(scores) = %d, want 2", got.Len())
	}

	byID := map[string]float64{}
	for i := range got {
		byID[got[i].ID()] = got[i].Score
	}
	if byID["node-full"] <= byID["node-empty"] {
		t.Fatalf("scores = %+v, want node-full > node-empty", byID)
	}
}

func TestBinpackScoreRegisteredByConfig(t *testing.T) {
	initBinpackScoreTestConfig(t, `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - binpack_score
    resource_weights:
      binpack_score: 1
`)

	selectors := NewSelector(context.Background())
	if len(selectors) != 1 {
		t.Fatalf("len(selectors) = %d, want 1", len(selectors))
	}
	if selectors[0].ID() != constants.SelectorScoreID+"/"+binpackScoreName {
		t.Fatalf("selector ID = %s, want binpack_score", selectors[0].ID())
	}
}

func initBinpackScoreTestConfig(t *testing.T, yamlBody string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cubemaster.yaml")
	if err := os.WriteFile(path, []byte(yamlBody), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", path)
	if _, err := config.Init(); err != nil {
		t.Fatalf("config.Init(): %v", err)
	}
}

func binpackScoreTestCtx(t *testing.T) *selctx.SelectorCtx {
	t.Helper()
	empty := &node.Node{
		InsID:         "node-empty",
		QuotaCpu:      1000,
		QuotaMem:      1000,
		QuotaCpuUsage: 100,
		QuotaMemUsage: 100,
		MvmNum:        1,
		MaxMvmLimit:   10,
	}
	full := &node.Node{
		InsID:         "node-full",
		QuotaCpu:      1000,
		QuotaMem:      1000,
		QuotaCpuUsage: 800,
		QuotaMemUsage: 800,
		MvmNum:        8,
		MaxMvmLimit:   10,
	}
	selCtx := selctx.New("random")
	selCtx.Ctx = context.Background()
	selCtx.SetNodes(node.NodeList{empty, full})
	return selCtx
}
