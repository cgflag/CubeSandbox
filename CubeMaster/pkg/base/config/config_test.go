// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

// Package config provides the configuration for the cube master
package config

import (
	"fmt"

	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/constants"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/log"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
)

// testAbsPath builds a platform-absolute path for host-mount prefix tests.
// On Windows filepath.IsAbs("/data/...") is false, so Unix-style fixtures
// cannot express a "valid absolute path" there.
func testAbsPath(elems ...string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(`C:\`, filepath.Join(elems...))
	}
	return "/" + strings.Join(elems, "/")
}

func TestInit(t *testing.T) {
	mydir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	fmt.Printf("mydir=%s\n", mydir)
	if os.Getenv("CUBE_MASTER_CONFIG_PATH") == "" {
		configPath := filepath.Clean(filepath.Join(mydir, "../../../test/conf.yaml"))
		if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
			t.Skipf("skip TestInit: config fixture not found: %s", configPath)
		}
		os.Setenv("CUBE_MASTER_CONFIG_PATH", configPath)
	}
	_, err = Init()
	assert.NoError(t, err)
	assert.Equal(t, 2, len(GetConfig().ExtraConf.BlkQosMap))
	assert.Equal(t, 2, len(GetConfig().ExtraConf.FsQosMap))

	assert.NotNil(t, GetConfig().Scheduler)
	assert.NotNil(t, GetConfig().Scheduler.LargeSizeAffinityConf)
	cubeboxConf := GetConfig().Scheduler.LargeSizeAffinityConf["cubebox"]
	assert.NotNil(t, cubeboxConf)
	assert.Equal(t, true, cubeboxConf.Enable)
	expectMem := resource.MustParse("100Gi")
	gotMem, err := resource.ParseQuantity(cubeboxConf.MemoryLowerWaterMark)
	assert.NoError(t, err)
	assert.True(t, expectMem.Equal(gotMem))
	expectCpu := resource.MustParse("100000m")
	gotCpu, err := resource.ParseQuantity(cubeboxConf.CpuLowerWaterMark)
	assert.NoError(t, err)
	assert.True(t, expectCpu.Equal(gotCpu))
}

func TestNestedLogSettingsUseSnakeCaseOnRoundTrip(t *testing.T) {
	var cfg Config
	err := yaml.Unmarshal([]byte("log:\n  file_size: 100\n  file_num: 10\n  enable_log_metric: true\n"), &cfg)
	assert.NoError(t, err)
	if !assert.NotNil(t, cfg.Log) {
		return
	}

	assert.Equal(t, 100, cfg.Log.FileSize)
	assert.Equal(t, 10, cfg.Log.FileNum)
	assert.True(t, cfg.Log.EnableLogMetric)

	encoded, err := yaml.Marshal(&cfg)
	assert.NoError(t, err)
	text := string(encoded)
	assert.Contains(t, text, "file_size: 100")
	assert.Contains(t, text, "file_num: 10")
	assert.Contains(t, text, "enable_log_metric: true")
	assert.NotContains(t, text, "fileSize:")
	assert.NotContains(t, text, "fileNum:")
	assert.NotContains(t, text, "enableLogMetric:")
}

func TestGetEffectiveNodeMaxMemReservedInMBFallsBackForSmallNodes(t *testing.T) {
	sconf := &SchedulerConf{
		NodeMaxMemReservedInMB: 10 * 1024,
	}

	got := sconf.GetEffectiveNodeMaxMemReservedInMB("cubebox", 9450)
	assert.Equal(t, int64(945), got)
}

func TestGetEffectiveNodeMaxMemReservedInMBKeepsConfiguredValue(t *testing.T) {
	sconf := &SchedulerConf{
		NodeMaxMemReservedInMB: 512,
	}

	got := sconf.GetEffectiveNodeMaxMemReservedInMB("cubebox", 9450)
	assert.Equal(t, int64(512), got)
}

func TestPreHandleSchedulerIgnoreRedisAllocationDefault(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{}}
	err := preHandleScheduler(cfg)
	assert.NoError(t, err)

	assert.NotNil(t, cfg.Scheduler.IgnoreRedisAllocation)
	assert.False(t, cfg.Scheduler.ShouldIgnoreRedisAllocation())
}

func TestHasDeprecatedOvercommitConfig(t *testing.T) {
	assert.False(t, (&SchedulerConf{}).hasDeprecatedOvercommitConfig())

	assert.True(t, (&SchedulerConf{
		DeprecatedOvercommitRatio: &deprecatedOvercommitRatioConf{CPURatio: 3, MemRatio: 2},
	}).hasDeprecatedOvercommitConfig())

	assert.True(t, (&SchedulerConf{
		DeprecatedOvercommitRatioByType: map[string]deprecatedOvercommitRatioConf{
			"cubebox_gpu": {CPURatio: 1, MemRatio: 1},
		},
	}).hasDeprecatedOvercommitConfig())

	var nilConf *SchedulerConf
	assert.False(t, nilConf.hasDeprecatedOvercommitConfig())
}

func TestEffectiveAllocated(t *testing.T) {
	ignore := false
	sconf := &SchedulerConf{IgnoreRedisAllocation: &ignore}
	assert.Equal(t, int64(1234), sconf.EffectiveAllocated(1234))

	defaultConf := &SchedulerConf{}
	assert.Equal(t, int64(1234), defaultConf.EffectiveAllocated(1234))

	ignoreTrue := true
	ignoring := &SchedulerConf{IgnoreRedisAllocation: &ignoreTrue}
	assert.Equal(t, int64(0), ignoring.EffectiveAllocated(1234))
}

func TestNodeAffinitySelectorAllowedKeySet(t *testing.T) {
	sconf := &SchedulerConf{
		NodeAffinitySelectorAllowedKeys: []string{"gpu"},
	}

	allowed := sconf.NodeAffinitySelectorAllowedKeySet()
	assert.Contains(t, allowed, constants.AffinityKeyZone)
	assert.Contains(t, allowed, constants.AffinityKeyClusterID)
	assert.Contains(t, allowed, constants.AffinityKeyMemorySize)
	assert.Contains(t, allowed, "gpu")
	assert.NotContains(t, allowed, constants.AffinityKeyDisaterRecoverGroup)
}

func TestNodeAffinitySelectorAllowedKeySet_NilReceiver(t *testing.T) {
	var sconf *SchedulerConf
	allowed := sconf.NodeAffinitySelectorAllowedKeySet()
	assert.Contains(t, allowed, constants.AffinityKeyZone)
	assert.Contains(t, allowed, constants.AffinityKeyClusterID)
	assert.Contains(t, allowed, constants.AffinityKeyInstanceType)
	assert.NotContains(t, allowed, "gpu")
}

func TestDefaultNodeAffinitySelectorAllowedKeySet(t *testing.T) {
	allowed := DefaultNodeAffinitySelectorAllowedKeySet()
	assert.Contains(t, allowed, constants.AffinityKeyZone)
	assert.Contains(t, allowed, constants.AffinityKeyClusterID)
	assert.Contains(t, allowed, constants.AffinityKeyCPUType)
	assert.Contains(t, allowed, constants.AffinityKeyMemorySize)
	assert.Contains(t, allowed, constants.AffinityKeyCPUCores)
	assert.Contains(t, allowed, constants.AffinityKeyInstanceType)
	assert.NotContains(t, allowed, "gpu")
	assert.NotContains(t, allowed, constants.AffinityKeyDisaterRecoverGroup)
}

func TestValidateAllowedHostMountPrefixes(t *testing.T) {
	validShared := testAbsPath("data", "shared")
	validSharedSlash := validShared + string(filepath.Separator)
	validNFS := testAbsPath("mnt", "nfs") + string(filepath.Separator)

	tests := []struct {
		name     string
		prefixes []string
		wantErr  bool
	}{
		{"valid with trailing slash", []string{validSharedSlash}, false},
		{"valid without trailing slash", []string{validShared}, false},
		{"multiple valid", []string{validSharedSlash, validNFS}, false},
		// "/" is rejected on Unix as root; on Windows it fails filepath.IsAbs.
		{"reject root path /", []string{"/"}, true},
		// "/data/.." cleans to "/" on Unix; on Windows it fails filepath.IsAbs.
		{"reject root via traversal", []string{"/data/.."}, true},
		{"reject empty string", []string{""}, true},
		{"reject relative path", []string{"data/shared/"}, true},
		{"reject dot path", []string{"."}, true},
		{"one valid one invalid", []string{validSharedSlash, ""}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{
				Log: &log.Conf{},
				ExtraConf: &ExtraConf{
					AllowedHostMountPrefixes: tt.prefixes,
				},
			}
			err := validate(c)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetAllowedHostMountPrefixes_Default(t *testing.T) {
	old := cfg
	cfg = nil
	defer func() { cfg = old }()

	got := GetAllowedHostMountPrefixes()
	assert.Equal(t, []string{"/data/shared/"}, got)
}

func TestGetAllowedHostMountPrefixes_AutoAppendSlash(t *testing.T) {
	old := cfg
	cfg = &Config{
		ExtraConf: &ExtraConf{
			AllowedHostMountPrefixes: []string{"/data/shared", "/mnt/nfs/"},
		},
	}
	defer func() { cfg = old }()

	got := GetAllowedHostMountPrefixes()
	assert.Equal(t, []string{"/data/shared/", "/mnt/nfs/"}, got)
}

func TestGetAllowedHostMountPrefixes_DefensiveCopy(t *testing.T) {
	old := cfg
	cfg = &Config{
		ExtraConf: &ExtraConf{
			AllowedHostMountPrefixes: []string{"/data/shared/"},
		},
	}
	defer func() { cfg = old }()

	got := GetAllowedHostMountPrefixes()
	got[0] = "/hacked/"
	// original config must not be affected
	assert.Equal(t, "/data/shared/", cfg.ExtraConf.AllowedHostMountPrefixes[0])
}

func TestGetAllowedHostMountPrefixes_DefaultDefensiveCopy(t *testing.T) {
	old := cfg
	cfg = nil
	defer func() { cfg = old }()

	got := GetAllowedHostMountPrefixes()
	got[0] = "/hacked/"
	// package-level default must not be affected
	got2 := GetAllowedHostMountPrefixes()
	assert.Equal(t, "/data/shared/", got2[0])
}

func TestPreHandleScheduler_NoProfileLeavesSchedulerUnchanged(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{
		SchedulerConf: SchedulerConf{
			Filter: &SchedulerFilterConf{EnableFilters: []string{"cpu", "mem"}},
			Score: &SchedulerScoreConf{
				EnableScorers:   []string{"affinity_score"},
				ResourceWeights: map[string]float64{"cpu": 1.0},
			},
			Profiles: map[string]SchedulerProfileConf{
				"unused": {
					Filter: &SchedulerFilterConf{EnableFilters: []string{"disk"}},
				},
			},
		},
	}}

	err := preHandleScheduler(cfg)
	assert.NoError(t, err)
	assert.Equal(t, []string{"cpu", "mem"}, cfg.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"affinity_score"}, cfg.Scheduler.Score.EnableScorers)
	assert.Equal(t, map[string]float64{"cpu": 1.0}, cfg.Scheduler.Score.ResourceWeights)
}

func TestPreHandleScheduler_ProfileAppliesFilterAndScore(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{
		SchedulerConf: SchedulerConf{
			Profile: "spread_like",
			Filter:  &SchedulerFilterConf{EnableFilters: []string{"cpu"}},
			Score: &SchedulerScoreConf{
				EnableScorers:   []string{"affinity_score"},
				ResourceWeights: map[string]float64{"cpu": 1.0},
			},
			Profiles: map[string]SchedulerProfileConf{
				"spread_like": {
					Filter: &SchedulerFilterConf{
						EnableFilters: []string{"cpu", "mem", "realtime_create_num"},
					},
					Score: &SchedulerProfileScoreConf{
						EnableScorers:   []string{"real_time_weighted_average", "multi_factor_weighted_average"},
						ResourceWeights: map[string]float64{"cpu": 0.4, "mem": 0.6},
					},
				},
			},
		},
	}}

	err := preHandleScheduler(cfg)
	assert.NoError(t, err)
	assert.Equal(t, []string{"cpu", "mem", "realtime_create_num"}, cfg.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"real_time_weighted_average", "multi_factor_weighted_average"}, cfg.Scheduler.Score.EnableScorers)
	assert.Equal(t, map[string]float64{"cpu": 0.4, "mem": 0.6}, cfg.Scheduler.Score.ResourceWeights)
}

func TestPreHandleScheduler_UnknownProfileReturnsError(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{
		SchedulerConf: SchedulerConf{
			Profile: "missing_profile",
			Profiles: map[string]SchedulerProfileConf{
				"other": {},
			},
		},
	}}

	err := preHandleScheduler(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing_profile")
	assert.Contains(t, err.Error(), "not found")
}

func TestPreHandleScheduler_UnknownFilterInProfileReturnsError(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{
		SchedulerConf: SchedulerConf{
			Profile: "bad_filter",
			Profiles: map[string]SchedulerProfileConf{
				"bad_filter": {
					Filter: &SchedulerFilterConf{EnableFilters: []string{"cpu", "not_a_real_filter"}},
				},
			},
		},
	}}

	err := preHandleScheduler(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_real_filter")
	assert.Contains(t, err.Error(), "unknown filter")
}

func TestPreHandleScheduler_UnknownScoreInProfileReturnsError(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{
		SchedulerConf: SchedulerConf{
			Profile: "bad_score",
			Profiles: map[string]SchedulerProfileConf{
				"bad_score": {
					Score: &SchedulerProfileScoreConf{
						EnableScorers: []string{"affinity_score", "not_a_real_score"},
					},
				},
			},
		},
	}}

	err := preHandleScheduler(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_real_score")
	assert.Contains(t, err.Error(), "unknown score")
}

func TestPreHandleScheduler_ProfileFilterOnlyDoesNotClearScore(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{
		SchedulerConf: SchedulerConf{
			Profile: "filter_only",
			Filter:  &SchedulerFilterConf{EnableFilters: []string{"cpu"}},
			Score: &SchedulerScoreConf{
				EnableScorers:   []string{"image_score"},
				ResourceWeights: map[string]float64{"mem": 2.0},
			},
			Profiles: map[string]SchedulerProfileConf{
				"filter_only": {
					Filter: &SchedulerFilterConf{EnableFilters: []string{"cpu", "disk"}},
				},
			},
		},
	}}

	err := preHandleScheduler(cfg)
	assert.NoError(t, err)
	assert.Equal(t, []string{"cpu", "disk"}, cfg.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"image_score"}, cfg.Scheduler.Score.EnableScorers)
	assert.Equal(t, map[string]float64{"mem": 2.0}, cfg.Scheduler.Score.ResourceWeights)
}

func TestPreHandleScheduler_ProfileScoreOnlyDoesNotClearFilter(t *testing.T) {
	cfg := &Config{Scheduler: &WrapperSchedulerConf{
		SchedulerConf: SchedulerConf{
			Profile: "score_only",
			Filter:  &SchedulerFilterConf{EnableFilters: []string{"mem", "thirtparty"}},
			Score: &SchedulerScoreConf{
				EnableScorers:   []string{"affinity_score"},
				ResourceWeights: map[string]float64{"cpu": 1.0},
			},
			Profiles: map[string]SchedulerProfileConf{
				"score_only": {
					Score: &SchedulerProfileScoreConf{
						EnableScorers:   []string{"external_http_score"},
						ResourceWeights: map[string]float64{"cpu": 0.2, "mem": 0.8},
					},
				},
			},
		},
	}}

	err := preHandleScheduler(cfg)
	assert.NoError(t, err)
	assert.Equal(t, []string{"mem", "thirtparty"}, cfg.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"external_http_score"}, cfg.Scheduler.Score.EnableScorers)
	assert.Equal(t, map[string]float64{"cpu": 0.2, "mem": 0.8}, cfg.Scheduler.Score.ResourceWeights)
}

func TestAllowedSchedulerSelectorNamesMatchRegistries(t *testing.T) {
	// Keep these expectations aligned with filter/init.go and score/init.go.
	assert.Equal(t, map[string]struct{}{
		"cpu":                 {},
		"mem":                 {},
		"template_locality":   {},
		"realtime_create_num": {},
		"disk":                {},
		"thirtparty":          {},
	}, allowedSchedulerFilterNames)
	assert.Equal(t, map[string]struct{}{
		"real_time_weighted_average":    {},
		"multi_factor_weighted_average": {},
		"affinity_score":                {},
		"image_score":                   {},
		"external_http_score":           {},
		"binpack_score":                 {},
	}, allowedSchedulerScoreNames)
}

func yamlFieldNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func initConfigFromYAML(t *testing.T, yamlBody string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cubemaster.yaml")
	if err := os.WriteFile(path, []byte(yamlBody), 0644); err != nil {
		t.Fatalf("write config yaml: %v", err)
	}
	t.Setenv("CUBE_MASTER_CONFIG_PATH", path)
	old := cfg
	t.Cleanup(func() { cfg = old })
	return Init()
}

func TestPreHandleScheduler_ProfileExampleYAMLAppliesExternalHTTPScore(t *testing.T) {
	// Copyable runtime example from docs/dev/scheduler-profile-config-example.md.
	// plugin_conf.external_http_score stays on scheduler.score, not profile overlay.
	yamlBody := `common: {}
log: {}
scheduler:
  profile: http_score_combo
  profiles:
    http_score_combo:
      filter:
        enable_filters:
          - cpu
          - mem
          - template_locality
      score:
        enable_scorers:
          - image_score
          - external_http_score
        resource_weights:
          image_score: 1
          external_http_score: 2
  score:
    plugin_conf:
      external_http_score:
        weight: 1
        endpoint: "http://127.0.0.1:18080/score"
        timeout: 200ms
        mode: default
        disable: false
`
	got, err := initConfigFromYAML(t, yamlBody)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.NotNil(t, got.Scheduler)
	assert.Equal(t, "http_score_combo", got.Scheduler.Profile)
	assert.Equal(t, []string{"cpu", "mem", "template_locality"}, got.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"image_score", "external_http_score"}, got.Scheduler.Score.EnableScorers)
	assert.Equal(t, 2.0, got.Scheduler.Score.ResourceWeights["external_http_score"])
	assert.Equal(t, 1.0, got.Scheduler.Score.ResourceWeights["image_score"])

	plugin := got.Scheduler.Score.ScorePluginConf.ExternalHTTPScore
	if assert.NotNil(t, plugin) {
		assert.Equal(t, "http://127.0.0.1:18080/score", plugin.Endpoint)
		assert.Equal(t, "default", plugin.Mode)
		assert.Equal(t, 200*time.Millisecond, plugin.Timeout)
		assert.False(t, plugin.Disable)
		assert.Equal(t, 1.0, plugin.Weight)
	}

	assert.NotContains(t, yamlFieldNames(t, reflect.TypeOf(SchedulerProfileScoreConf{})), "plugin_conf")
	assert.Contains(t, yamlFieldNames(t, reflect.TypeOf(SchedulerScoreConf{})), "plugin_conf")
}

func TestInit_EmptySchedulerProfileLeavesDirectConfigUnchanged(t *testing.T) {
	yamlBody := `common: {}
log: {}
scheduler:
  filter:
    enable_filters:
      - cpu
      - mem
  score:
    enable_scorers:
      - affinity_score
    resource_weights:
      cpu: 1
    plugin_conf:
      external_http_score:
        endpoint: "http://127.0.0.1:18080/score"
        timeout: 200ms
        mode: default
        disable: false
  profiles:
    unused:
      filter:
        enable_filters:
          - disk
`
	got, err := initConfigFromYAML(t, yamlBody)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, "", got.Scheduler.Profile)
	assert.Equal(t, []string{"cpu", "mem"}, got.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"affinity_score"}, got.Scheduler.Score.EnableScorers)
	assert.Equal(t, map[string]float64{"cpu": 1}, got.Scheduler.Score.ResourceWeights)
	plugin := got.Scheduler.Score.ScorePluginConf.ExternalHTTPScore
	if assert.NotNil(t, plugin) {
		assert.Equal(t, "http://127.0.0.1:18080/score", plugin.Endpoint)
		assert.Equal(t, "default", plugin.Mode)
		assert.Equal(t, 200*time.Millisecond, plugin.Timeout)
		assert.False(t, plugin.Disable)
	}
}

func TestPreHandleScheduler_BuiltinProfilesApplyWithoutUserMap(t *testing.T) {
	cases := []struct {
		name            string
		wantFilters     []string
		wantScorers     []string
		wantWeightKey   string
		wantWeightValue float64
	}{
		{
			name:            "balanced_spread",
			wantFilters:     []string{"cpu", "mem", "realtime_create_num"},
			wantScorers:     []string{"real_time_weighted_average"},
			wantWeightKey:   "realtime_create_num",
			wantWeightValue: 2,
		},
		{
			name:            "template_locality_first",
			wantFilters:     []string{"cpu", "mem", "template_locality"},
			wantScorers:     []string{"image_score"},
			wantWeightKey:   "template_id",
			wantWeightValue: 2,
		},
		{
			name:            "binpack_utilization",
			wantFilters:     []string{"cpu", "mem"},
			wantScorers:     []string{"binpack_score"},
			wantWeightKey:   "binpack_score",
			wantWeightValue: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yamlBody := fmt.Sprintf(`common: {}
log: {}
scheduler:
  profile: %s
`, tc.name)
			got, err := initConfigFromYAML(t, yamlBody)
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tc.name, got.Scheduler.Profile)
			assert.Equal(t, tc.wantFilters, got.Scheduler.Filter.EnableFilters)
			assert.Equal(t, tc.wantScorers, got.Scheduler.Score.EnableScorers)
			assert.Equal(t, tc.wantWeightValue, got.Scheduler.Score.ResourceWeights[tc.wantWeightKey])
		})
	}
}

func TestPreHandleScheduler_UnknownProfileStillFailsClosed(t *testing.T) {
	yamlBody := `common: {}
log: {}
scheduler:
  profile: not_a_builtin_or_user_profile
  profiles:
    http_score_combo:
      filter:
        enable_filters:
          - cpu
`
	_, err := initConfigFromYAML(t, yamlBody)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_builtin_or_user_profile")
	assert.Contains(t, err.Error(), "not found")
}

func TestPreHandleScheduler_UserProfileOverridesBuiltin(t *testing.T) {
	yamlBody := `common: {}
log: {}
scheduler:
  profile: balanced_spread
  profiles:
    balanced_spread:
      filter:
        enable_filters:
          - disk
      score:
        enable_scorers:
          - affinity_score
        resource_weights:
          cpu: 9
`
	got, err := initConfigFromYAML(t, yamlBody)
	assert.NoError(t, err)
	assert.Equal(t, []string{"disk"}, got.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"affinity_score"}, got.Scheduler.Score.EnableScorers)
	assert.Equal(t, map[string]float64{"cpu": 9}, got.Scheduler.Score.ResourceWeights)
}

func TestInit_EmptySchedulerProfileDoesNotApplyBuiltin(t *testing.T) {
	yamlBody := `common: {}
log: {}
scheduler:
  filter:
    enable_filters:
      - cpu
      - mem
  score:
    enable_scorers:
      - affinity_score
    resource_weights:
      cpu: 1
`
	got, err := initConfigFromYAML(t, yamlBody)
	assert.NoError(t, err)
	assert.Equal(t, "", got.Scheduler.Profile)
	assert.Equal(t, []string{"cpu", "mem"}, got.Scheduler.Filter.EnableFilters)
	assert.Equal(t, []string{"affinity_score"}, got.Scheduler.Score.EnableScorers)
	assert.Equal(t, map[string]float64{"cpu": 1}, got.Scheduler.Score.ResourceWeights)
}
