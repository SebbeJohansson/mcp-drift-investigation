# MCP drift investigation (AI-113)

Prototype for detecting upstream API drift under an MCP server and spawning an agent to fix it. Go 1.25+, official MCP Go SDK.

## Layout

| Path | What |
| -- | -- |
| `internal/mockapi/` | Mock shop REST API with chaos switches and a spec that follows them |
| `internal/shopmcp/` | MCP server wrapping the API: `list_products`, `get_product`, `create_order`, `get_order` |
| `internal/contracts/` | Contract runner: calls every tool, validates output against `schemas/<tool>.json` |
| `internal/detector/` | Drift detector: spec diff, tools/list diff, contracts, health → drift event |
| `baseline/baseline.json` | Known-good OpenAPI spec + tools/list the detector compares against |
| `cmd/mockapi` | Run the API (`:8100`) |
| `cmd/mockmcp` | Run the MCP server over stdio (`SHOP_API_URL`, `SHOP_API_KEY`) |
| `cmd/chaos` | Toggle chaos switches on a running API |
| `cmd/snapshot` | Regenerate the contract schemas from the current server |
| `cmd/detector` | Run the detector once or on an interval; writes `events/<fingerprint>.json` |

## Run

```bash
go test ./...                          # baseline passes, every chaos switch breaks a contract
go run ./cmd/mockapi                   # run the API
go run ./cmd/chaos rename_field        # turn on a switch (--reset to clear)
go run ./cmd/detector                  # check once: exit 0 none, 2 spawn_agent, 3 escalate
go run ./cmd/detector -interval 1m -on-drift ./some-hook.sh   # poll, run hook per new event
go run ./cmd/detector -update-baseline # accept the current state as known-good
```

## Detection

Each run compares against the baseline and produces a drift event with a list of changes, each marked breaking or not:

- **openapi**: removed operations or response codes, removed/retyped response fields, new required request fields, auth scheme changes. Single remove+add in one schema is flagged as a possible rename.
- **tools**: tools removed or input/output schema changed in `tools/list`.
- **contracts**: any failing contract case.
- **health**: authenticated probe; unreachable, timeout, 5xx, 401/403, other 4xx, slow.

Action: `none` if nothing breaking. `escalate` for outages (5xx, timeouts) and for 401s while the auth scheme is unchanged (rotated secret). Otherwise `spawn_agent`. The fingerprint is a hash of the breaking changes, so the same drift is reported once.

## Chaos switches

| Switch | Spec changes | Contracts that break | Detector action |
| -- | -- | -- | -- |
| `rename_field` | yes | list_products, get_product | spawn_agent |
| `remove_field` | yes | list_products, get_product | spawn_agent |
| `required_param` | yes | create_order, get_order | spawn_agent |
| `path_v2` | yes | all | spawn_agent |
| `auth_bearer` | yes | all | spawn_agent |
| `rotate_key` | no | all (needs a secret, agent should escalate) | escalate |
| `error_format` | yes | get_product_not_found | spawn_agent |
| `flaky_5xx` | no | all (health issue, not code drift) | escalate |
| `slow` | no | all (timeouts) | escalate |
