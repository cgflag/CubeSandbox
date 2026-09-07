---
title: Scheduler Profile Configuration Example
description: Copyable CubeMaster runtime scheduler.profile / scheduler.profiles YAML examples, including built-in presets. Runtime overlay is not equivalent to offline simulator strategy profiles.
status: working_guide
source: CubeMaster/pkg/base/config/config.go
updated: 2026-09-06
---

# Scheduler Profile Configuration Example

Copyable YAML for CubeMaster **runtime** Profile overlay. HTTP scorer protocol:
`docs/dev/external-http-score.md`.

## Scope

This document shows how to set `scheduler.profile` and `scheduler.profiles` so
config `preHandle` applies Filter/Score selector lists and merges Profile
`resource_weights` over the base `scheduler.score.resource_weights` map.

In scope:

- user-defined runtime Profile overlay;
- built-in presets `balanced_spread`, `template_locality_first`,
  `binpack_utilization` (empty `scheduler.profile` still leaves defaults);
- enabling `external_http_score` by name in a profile;
- keeping `plugin_conf` on `scheduler.score.plugin_conf`.

Out of scope:

- treating runtime presets as formula-equivalent to simulator
  `weightsForProfile`;
- putting `plugin_conf` under `scheduler.profiles.<name>.score`;
- changing production Filter/Score defaults when `scheduler.profile` is empty.

## Runtime Profile Contract

Source: `CubeMaster/pkg/base/config/config.go`
(`SchedulerConf`, `SchedulerProfileConf`, `SchedulerProfileScoreConf`,
`applySchedulerProfile`, `builtinSchedulerProfiles`,
`validateSchedulerProfileSelectors`).

| YAML path | Role |
|---|---|
| `scheduler.profile` | Name of the active overlay. Empty string (default) means no expansion. Built-in names apply without a user map key. |
| `scheduler.profiles` | User-defined map of named overlays. A user key with the same name as a built-in **overrides** the built-in. |
| `scheduler.profiles.<name>.filter.enable_filters` | Copied onto `scheduler.filter.enable_filters`. |
| `scheduler.profiles.<name>.score.enable_scorers` | Copied onto `scheduler.score.enable_scorers`. |
| `scheduler.profiles.<name>.score.resource_weights` | Merged over `scheduler.score.resource_weights`; Profile keys win and unrelated base keys remain. These are factor weights, not plugin weights. |
| `scheduler.score.plugin_conf.*` | Per-scorer params. **Not** a profile overlay field. |

A runtime Profile is a **selector overlay**. It does not change
`Select()` phase order. Built-in presets are scene-oriented combinations of
existing filters/scores (plus thin `binpack_score`). They are **not**
simulator built-in strategy weights.

`SchedulerProfileScoreConf` intentionally omits `plugin_conf`. Every registered
scorer listed in the final effective `enable_scorers` requires its corresponding
`scheduler.score.plugin_conf` block; missing configuration fails before
scheduler construction. This applies to direct configuration and scorers
inherited through a partial Profile as well as scorers listed by a user
Profile. The three built-ins inject self-contained defaults for their own
scorers: `balanced_spread` for `real_time_weighted_average`,
`template_locality_first` for `image_score`, and `binpack_utilization` for
`binpack_score`.

Factor-based `real_time_weighted_average`,
`multi_factor_weighted_average`, and `image_score` are constructed only when
at least one of their `enable_weight_factors` has a positive
`resource_weights` value. `affinity_score`, `external_http_score`, and
`binpack_score` do not use that map as a construction gate.

Unknown `scheduler.profile` names (not user-defined and not built-in) and
unknown filter/score names in the selected overlay fail closed in
`preHandleScheduler` before the scheduler runs.

Allowed filter names (must match `CubeMaster/pkg/selector/filter/init.go`):

- `cpu`
- `mem`
- `template_locality`
- `realtime_create_num`
- `disk`
- `thirtparty`

Allowed score names (must match `CubeMaster/pkg/selector/score/init.go`):

- `real_time_weighted_average`
- `multi_factor_weighted_average`
- `affinity_score`
- `image_score`
- `external_http_score`
- `binpack_score`

## Built-in Profile Examples

Leave `scheduler.profile` empty to keep the current Filter/Score config.
Setting a built-in name does **not** require a matching key under
`scheduler.profiles`.

`balanced_spread` (high-concurrency short-lived sandboxes) supplies a default
`real_time_weighted_average` block:

```yaml
scheduler:
  profile: balanced_spread
```

`template_locality_first` (repeated same-template creates) likewise supplies
safe `image_score` defaults:

```yaml
scheduler:
  profile: template_locality_first
```

`binpack_utilization` (mixed-size / long-lived) injects plugin weight 1 and
equal CPU/memory/MVM occupancy weights when its plugin block is omitted:

```yaml
scheduler:
  profile: binpack_utilization
```

These overlays are scene-oriented selector combinations. They are **not**
the same as simulator `weightsForProfile`.

## Minimal Profile Example

Leave `scheduler.profile` empty to keep the current Filter/Score config.
When a name is set, it must exist in `scheduler.profiles`. The name below
(`locality_combo`) is an operator-chosen key, not a CubeMaster built-in.

```yaml
scheduler:
  profile: locality_combo
  profiles:
    locality_combo:
      filter:
        enable_filters:
          - cpu
          - mem
          - template_locality
      score:
        enable_scorers:
          - image_score
          - affinity_score
        resource_weights:
          image_id: 1
          template_id: 2
  score:
    plugin_conf:
      image_score:
        weight: 1
        enable_weight_factors:
          - image_id
          - template_id
      affinity_score:
        weight: 1
```

What this overlay copies at `preHandle`:

- `scheduler.filter.enable_filters` becomes `cpu`, `mem`, `template_locality`;
- `scheduler.score.enable_scorers` becomes `image_score`, `affinity_score`;
- the Profile factor weights merge over existing
  `scheduler.score.resource_weights`; plugin weights remain under
  `plugin_conf`.

Omitted overlay sections are left untouched. A filter-only profile does not
clear existing `enable_scorers`; a score-only profile does not clear existing
`enable_filters`.

## ExternalHTTPScore Profile Example

A profile may **enable** `external_http_score` by listing it in
`enable_scorers`. The HTTP plugin weight and parameters
must still be set on `scheduler.score.plugin_conf.external_http_score`.
Do **not** put `plugin_conf` under `scheduler.profiles.<name>.score`.

```yaml
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
          - external_http_score
  score:
    plugin_conf:
      external_http_score:
        weight: 1
        endpoint: "http://127.0.0.1:18080/score"
        timeout: 200ms
        mode: default
        disable: false
```

If `external_http_score` is listed in the final `enable_scorers` but
`plugin_conf.external_http_score` is omitted, configuration fails fast.
Merely placing `external_http_score` in `resource_weights` neither enables nor
weights the plugin. For every scorer, explicit `weight: 0` disables it and its
`Select` method is skipped. For binpack compatibility, a negative plugin weight
retains the previous `<= 0` fallback value of 1; only exact zero has the new
disable meaning. Protocol fields and demo endpoint:
`docs/dev/external-http-score.md`.

Invalid (will not overlay `plugin_conf`; the Go type has no such field):

```yaml
# Do not do this. scheduler.profiles.<name>.score has no plugin_conf.
# HTTP endpoint/timeout stay on scheduler.score.plugin_conf.external_http_score.
scheduler:
  profiles:
    http_score_combo:
      score:
        enable_scorers:
          - external_http_score
        plugin_conf:          # not a SchedulerProfileScoreConf field
          external_http_score:
            endpoint: "http://127.0.0.1:18080/score"
```

## Runtime Profile vs Simulator Profile

Runtime Profile and simulator strategy profiles share the word "profile"
but are **not equivalent**.

| | Runtime Profile | Simulator strategy profile |
|---|---|---|
| Where | CubeMaster config: `scheduler.profile` / `scheduler.profiles` | Offline `schedulerbench` / `pkg/scheduler/simulator` `weightsForProfile` |
| What it is | User-defined overlay of existing filter/score selector names and factor `resource_weights` | Scoring-weight preset inside the offline placement model |
| Built-in names | `balanced_spread`, `template_locality_first`, `binpack_utilization` (selector overlay; user map key overrides). Operators may also choose other map keys. | **simulator-only weights:** `default`, `balanced_spread`, `template_locality_first`, `binpack_utilization` |
| `plugin_conf` | Not overlayable. HTTP params stay on `scheduler.score.plugin_conf` | Not CubeMaster scheduler YAML |

**Runtime built-in preset names** (selector overlay, not simulator weights):

- `balanced_spread`
- `template_locality_first`
- `binpack_utilization`

Copying one of those strings into `scheduler.profile` now loads the CubeMaster
built-in overlay unless you also define a matching user key under
`scheduler.profiles` (user wins). Simulator workloads and `--verify` live in
`docs/dev/scheduler-simulator-benchmark.md`. They do not prove a real
multi-node deployment, CubeAPI/Cubelet create path, real create latency, or
production performance.

## Do / Do Not

**Do**

- User-define extra profile map keys (`locality_combo`, `http_score_combo`, or any
  other operator-chosen name).
- Use only registered selector names listed above.
- Keep `plugin_conf` on `scheduler.score.plugin_conf`.
- Put scorer/plugin weights under `plugin_conf.<scorer>.weight`; do not use a
  scorer name as a `resource_weights` key.
- Leave `scheduler.profile` empty when you want existing Filter/Score config
  unchanged.
- Treat runtime built-in presets and simulator `weightsForProfile` as two
  paths that share names but are **not** formula-equivalent.

**Do Not**

- Claim runtime presets use the same scoring formula as the offline simulator.
- Put `plugin_conf` under `scheduler.profiles.<name>.score`.
- Invent filter names (`cpu`, `mem`, `template_locality`,
  `realtime_create_num`, `disk`, `thirtparty` are the allowed set) or score
  names (`real_time_weighted_average`, `multi_factor_weighted_average`,
  `affinity_score`, `image_score`, `external_http_score`, `binpack_score` are
  the allowed set).
- Equate runtime `scheduler.profiles` with simulator `weightsForProfile`.
- Treat a Profile overlay as a change to
  `PreFilter -> Filter -> Score -> PostScore`.

## Verification

These checks confirm the YAML contract against current source. They are not
live-cluster proof.

From the repository root:

```bash
rg -n "scheduler.profile|scheduler.profiles" CubeMaster/pkg/base/config/config.go
rg -n "external_http_score" CubeMaster/pkg/selector/score/init.go CubeMaster/pkg/base/config/config.go
```

From `CubeMaster`:

```bash
go test ./pkg/base/config
```

Relevant tests:

- `TestPreHandleScheduler_NoProfileLeavesSchedulerUnchanged`
- `TestPreHandleScheduler_ProfileAppliesFilterAndScore`
- `TestPreHandleScheduler_UnknownProfileReturnsError`
- `TestPreHandleScheduler_UnknownFilterInProfileReturnsError`
- `TestPreHandleScheduler_UnknownScoreInProfileReturnsError`
- `TestAllowedSchedulerSelectorNamesMatchRegistries`
- `TestPreHandleScheduler_BuiltinProfilesApplyWithoutUserMap`
- `TestPreHandleScheduler_UserProfileOverridesBuiltin`
