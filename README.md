# MCP drift investigation (AI-113)

Prototype for detecting upstream API drift under an MCP server and spawning an agent to fix it. Go 1.25+, official MCP Go SDK.

## Layout

| Path | What |
| -- | -- |
| `internal/mockapi/` | Mock shop REST API with chaos switches and a spec that follows them |
| `internal/shopmcp/` | MCP server wrapping the API: `list_products`, `get_product`, `create_order`, `get_order` |
| `internal/contracts/` | Contract runner: calls every tool, validates output against `schemas/<tool>.json` |
| `cmd/mockapi` | Run the API (`:8100`) |
| `cmd/mockmcp` | Run the MCP server over stdio (`SHOP_API_URL`, `SHOP_API_KEY`) |
| `cmd/chaos` | Toggle chaos switches on a running API |
| `cmd/snapshot` | Regenerate the contract schemas from the current server |

## Run

```bash
go test ./...                          # baseline passes, every chaos switch breaks a contract
go run ./cmd/mockapi                   # run the API
go run ./cmd/chaos rename_field        # turn on a switch (--reset to clear)
```

## Chaos switches

| Switch | Spec changes | Contracts that break |
| -- | -- | -- |
| `rename_field` | yes | list_products, get_product |
| `remove_field` | yes | list_products, get_product |
| `required_param` | yes | create_order, get_order |
| `path_v2` | yes | all |
| `auth_bearer` | yes | all |
| `rotate_key` | no | all (needs a secret, agent should escalate) |
| `error_format` | yes | get_product_not_found |
| `flaky_5xx` | no | all (health issue, not code drift) |
| `slow` | no | all (timeouts) |
