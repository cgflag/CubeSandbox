# External HTTP Score Plugin

`external_http_score` lets CubeMaster call an external HTTP service during the scheduler Score phase. The service returns a 0-100 score for every candidate node, and CubeMaster feeds those scores into the existing weighted score pipeline.

The plugin is disabled unless it is listed in `scheduler.score.enable_scorers`. Unknown names in `enable_scorers` are skipped with a warning so a typo is visible without blocking other valid scorers. It does not run as a Filter, does not remove candidates, and does not directly choose the final node.

## Configuration

```yaml
scheduler:
  score:
    enable_scorers:
      - external_http_score
    plugin_conf:
      external_http_score:
        weight: 1
        endpoint: "http://127.0.0.1:18080/score"
        timeout: 200ms
        mode: default
        disable: false
```

Fields:

- `weight`: score plugin weight used by the existing weighted score pipeline.
  Explicit zero disables the scorer and prevents an HTTP request.
- `endpoint`: HTTP endpoint to call. Empty means the scorer is skipped.
- `timeout`: per-request timeout. Values less than or equal to zero use the built-in default of `200ms`.
- `mode`: opaque string passed to the external service so it can select a scoring policy.
- `disable`: secondary switch. When true, the scorer is skipped.

`resource_weights` contains factor weights and does not set this plugin's
weight. Whenever the final effective `enable_scorers` contains
`external_http_score`, omitting `plugin_conf.external_http_score` is a
configuration error. The scorer sends HTTP requests only when its name,
configuration, endpoint, and non-zero weight are present and it is not
disabled.

## Request

CubeMaster sends an HTTP `POST` request with `Content-Type: application/json`.

```json
{
  "mode": "default",
  "instance_type": "cubebox",
  "template_id": "tpl-example",
  "nodes": [
    {
      "node_id": "node-a",
      "node_ip": "10.0.0.1",
      "instance_type": "cubebox",
      "mvm_num": 10,
      "real_time_create_num": 1,
      "local_create_num": 1,
      "create_concurrent_num": 30,
      "quota_cpu": 64000,
      "quota_mem": 131072,
      "quota_cpu_usage": 20000,
      "quota_mem_usage": 32768,
      "cpu_util": 30,
      "mem_usage": 65536
    }
  ]
}
```

`request_id` is reserved in the protocol structure but is not currently populated by the scheduler context.

## Response

```json
{
  "scores": {
    "node-a": 80,
    "node-b": 60
  }
}
```

Validation rules:

- Candidate node IDs must be non-empty and unique before CubeMaster sends the request.
- The response must include exactly the same node set as the candidate set.
- Unknown nodes are rejected.
- Missing nodes are rejected.
- Scores must be finite numbers.
- Scores must be in the inclusive range `[0, 100]`.
- Response bodies larger than 1 MiB are rejected.

If the external request fails or the response is invalid, CubeMaster returns an error from this scorer. The existing `runScoreFilter` path skips failed scorers and continues with other scorers or the default selection path.

## Demo

Run the mock scorer:

```bash
cd CubeMaster
go run ./examples/external-http-score
```

Then point `plugin_conf.external_http_score.endpoint` to:

```text
http://127.0.0.1:18080/score
```

The demo scores nodes by preferring lower CPU utilization, lower memory usage, and lower real-time create pressure.
