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
config `preHandle` copies existing Filter/Score selector lists and
`resource_weights` onto `scheduler.filter` / `scheduler.score`.

In scope:

- user-defined runtime Profile overlay;
- built-in presets `balanced_spread`, `template_locality_first`,
  `binpack_utilization` (empty `scheduler.profile` still leaves defaults);
- enabling `external_http_score` **by name/weight** in a profile;
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
| `scheduler.profiles.<name>.score.resource_weights` | Copied onto `scheduler.score.resource_weights`. |
| `scheduler.score.plugin_conf.*` | Per-scorer params. **Not** a profile overlay field. |

A runtime Profile is a **selector overlay**. It does not change
`Select()` phase order. Built-in presets are scene-oriented combinations of
existing filters/scores (plus thin `binpack_score`). They are **not**
simulator built-in strategy weights.

`SchedulerProfileScoreConf` intentionally omits `plugin_conf`. A profile can
list `external_http_score` or `real_time_weighted_average` / `image_score` in
`enable_scorers`, but HTTP and those scorers' `plugin_conf` stay on
`scheduler.score.plugin_conf`. Missing `plugin_conf` for `real_time_weighted_average`
or `image_score` still panics at selector construction (existing behavior).
`binpack_score` uses safe defaults when `plugin_conf.binpack_score` is omitted.

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
`scheduler.profiles`. Keep `plugin_conf` for `real_time_weighted_average` and
`image_score` on `scheduler.score` if those scorers are enabled.

`balanced_spread` (high-concurrency short-lived sandboxes). Requires existing
`plugin_conf.real_time_weighted_average` or CubeMaster panics when constructing
that scorer:

```yaml
scheduler:
  profile: balanced_spread
  score:
    plugin_conf:
      real_time_weighted_average:
        weight: 1
        enable_weight_factors:
          - realtime_create_num
          - mvm_num
          - cpu_util
          - quota_cpu_usage
          - quota_mem_usage
```

`template_locality_first` (repeated same-template creates). Requires existing
`plugin_conf.image_score`:

```yaml
scheduler:
  profile: template_locality_first
  score:
    plugin_conf:
      image_score:
        weight: 1
        enable_weight_factors:
          - image_id
          - template_id
```

`binpack_utilization` (mixed-size / long-lived). `binpack_score` stays enabled
with equal CPU/mem/MVM occupancy weights when `plugin_conf.binpack_score` is
omitted:

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
          image_score: 1
          affinity_score: 1
```

What this overlay copies at `preHandle`:

- `scheduler.filter.enable_filters` becomes `cpu`, `mem`, `template_locality`;
- `scheduler.score.enable_scorers` becomes `image_score`, `affinity_score`;
- `scheduler.score.resource_weights` becomes the map above.

Omitted overlay sections are left untouched. A filter-only profile does not
clear existing `enable_scorers`; a score-only profile does not clear existing
`enable_filters`.

## ExternalHTTPScore Profile Example

A profile may **enable** `external_http_score` by listing it in
`enable_scorers` (and optionally weighting it). The HTTP plugin parameters
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
```

If `external_http_score` is listed in `enable_scorers` but
`plugin_conf.external_http_score` is omitted, CubeMaster constructs a
disabled scorer (no HTTP). Protocol fields and demo endpoint:
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
| What it is | User-defined overlay of existing filter/score selector names and `resource_weights` | Scoring-weight preset inside the offline placement model |
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
