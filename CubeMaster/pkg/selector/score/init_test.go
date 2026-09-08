// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package score

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/config"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/constants"
)

func TestBuiltinProfilesConstructWithoutPluginConfig(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	tests := []struct {
		profile string
		wantID  string
	}{
		{profile: config.RuntimeProfileBalancedSpread, wantID: "real_time_weighted_average"},
		{profile: config.RuntimeProfileTemplateLocalityFirst, wantID: "image_score"},
	}

	for _, tt := range tests {
		t.Run(tt.profile, func(t *testing.T) {
			initSelectorTestConfig(t, fmt.Sprintf(`common: {}
log: {}
scheduler:
  profile: %s
`, tt.profile))

			var selectors []Selector
			if panicked := didPanic(func() {
				selectors = NewSelector(context.Background())
			}); panicked {
				t.Fatalf("NewSelector() panicked for built-in profile %q", tt.profile)
			}
			if len(selectors) != 1 {
				t.Fatalf("len(selectors) = %d, want 1", len(selectors))
			}
			wantID := constants.SelectorScoreID + "/" + tt.wantID
			if selectors[0].ID() != wantID {
				t.Fatalf("selector ID = %q, want %q", selectors[0].ID(), wantID)
			}
		})
	}
}

func TestPluginScorerConstructsWithoutResourceWeights(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	initSelectorTestConfig(t, `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - external_http_score
    plugin_conf:
      external_http_score:
        weight: 0.25
        endpoint: "http://127.0.0.1:18080/score"
`)

	selectors := NewSelector(context.Background())
	if len(selectors) != 1 {
		t.Fatalf("len(selectors) = %d, want 1", len(selectors))
	}
	if selectors[0].Weight() != 0.25 {
		t.Fatalf("Weight() = %v, want plugin_conf weight 0.25", selectors[0].Weight())
	}
}

func TestResourceWeightsPluginNameDoesNotOverridePluginWeight(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	initSelectorTestConfig(t, `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - external_http_score
    resource_weights:
      external_http_score: 99
    plugin_conf:
      external_http_score:
        weight: 0.25
        endpoint: "http://127.0.0.1:18080/score"
`)

	selectors := NewSelector(context.Background())
	if len(selectors) != 1 {
		t.Fatalf("len(selectors) = %d, want 1", len(selectors))
	}
	if selectors[0].Weight() != 0.25 {
		t.Fatalf("Weight() = %v, want plugin_conf weight 0.25", selectors[0].Weight())
	}
}

func TestSelectorConstructionSeparatesFactorAndPluginScorers(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	for _, resourceWeights := range []string{"", "    resource_weights: {}\n"} {
		name := "nil"
		if resourceWeights != "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			// Plugin-only scorers still construct without ResourceWeights (DEC-011).
			initSelectorTestConfig(t, `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - affinity_score
      - external_http_score
      - binpack_score
`+resourceWeights+`    plugin_conf:
      affinity_score:
        weight: 1
      external_http_score:
        weight: 1
        endpoint: "http://127.0.0.1:18080/score"
      binpack_score:
        weight: 1
`)

			selectors := NewSelector(context.Background())
			got := make([]string, 0, len(selectors))
			for _, selector := range selectors {
				got = append(got, selector.ID())
			}
			want := []string{
				constants.SelectorScoreID + "/affinity_score",
				constants.SelectorScoreID + "/external_http_score",
				constants.SelectorScoreID + "/binpack_score",
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("selector IDs = %v, want plugin-only scorers %v", got, want)
			}
		})
	}
}

func TestFactorScorerRequiresWeightForEnabledFactor(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	path := filepath.Join(t.TempDir(), "cubemaster.yaml")
	content := `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - image_score
    resource_weights:
      mvm_num: 1
    plugin_conf:
      image_score:
        weight: 1
        enable_weight_factors: [image_id]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", path)
	_, err := config.Init()
	if err == nil {
		t.Fatal("config.Init() error = nil, want fail-fast for missing positive factor weight")
	}
	if !strings.Contains(err.Error(), "no positive resource weight") {
		t.Fatalf("config.Init() error = %v, want substring %q", err, "no positive resource weight")
	}
}

func TestZeroPluginWeightDisablesEveryScorer(t *testing.T) {
	if runIsolatedScoreConfigTest(t) {
		return
	}
	initSelectorTestConfig(t, `common: {}
log: {}
scheduler:
  score:
    enable_scorers:
      - real_time_weighted_average
      - multi_factor_weighted_average
      - image_score
      - affinity_score
      - external_http_score
      - binpack_score
    resource_weights:
      mvm_num: 1
      image_id: 1
    plugin_conf:
      real_time_weighted_average:
        weight: 0
        enable_weight_factors: [mvm_num]
      multi_factor_weighted_average:
        weight: 0
        enable_weight_factors: [mvm_num]
      image_score:
        weight: 0
        enable_weight_factors: [image_id]
      affinity_score:
        weight: 0
      external_http_score:
        weight: 0
        endpoint: "http://127.0.0.1:18080/score"
      binpack_score:
        weight: 0
`)

	selectors := NewSelector(context.Background())
	if len(selectors) != 6 {
		t.Fatalf("len(selectors) = %d, want 6", len(selectors))
	}
	for _, selector := range selectors {
		if !selector.Disable() {
			t.Errorf("%s Disable() = false, want true for weight: 0", selector.ID())
		}
	}
}

func initSelectorTestConfig(t *testing.T, yamlBody string) {
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

func didPanic(fn func()) (panicked bool) {
	defer func() {
		panicked = recover() != nil
	}()
	fn()
	return false
}
