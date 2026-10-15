# CLAUDE.md - zcx

Go CLI for the **Zipchat Backend API**. Dual-audience output (human tables / LLM JSON), built on [`clicore`](https://github.com/nicolasacchi/clicore). Fleet sibling of `pmx`; mirror its idioms. User docs: `README.md`. API reference: the [Zipchat Backend API docs](https://docs.zipchat.ai/installation-and-setup/chat-settings/integrations/backend-api) (append `.md` for markdown; a local copy at `docs/zipchat-backend-api.md` is gitignored, do not commit vendor docs).

**API**: base `https://app.zipchat.ai/api/integrations/backend_api/v1`, `Authorization: Bearer <token>` (`zc-` one chat, `zcu-` user). Rate limit 120 req / 60 s.

## HARD RULES

- **The bot is LIVE with customers. Never send PATCH, POST, PUT or DELETE to app.zipchat.ai** from tests, scripts or manual runs. GET is fine. Prompt write paths (`prompt push`/`restore`) are tested ONLY against `httptest` servers. Live, `prompt push` may only run with `--dry-run`.
- Never print, log or write the token (`ZIPCHAT_API_TOKEN`, `~/.config/zipchat/env`).
- **Fixtures must be synthetic.** Never copy real conversation content into the repo: it contains customer PII. The local cache (`~/.local/share/zcx`) is PII too.
- Tests point the client at `httptest` via `ZCX_BASE_URL` and use temp `XDG_DATA_HOME`/HOME.

## Config resolution (flag > env > file)

Token: `--token` > `ZIPCHAT_API_TOKEN` > `~/.config/zipchat/env` > `~/.config/zcx/config.toml`. Chat: `--chat` > `ZCX_CHAT_ID` > config > auto-detect (exactly one chat). Base URL: `ZCX_BASE_URL`. Data dir: `$XDG_DATA_HOME/zcx` or `~/.local/share/zcx` (dirs 0700, files 0600). Business hours in `config.toml` `[hours]`, default Mon-Fri 09:00-19:00 Europe/Rome. Human times Europe/Rome, API times UTC.

## Output and exit codes

TTY: table. Non-TTY/`--json`/`--jq`: JSON (`--jq` is gjson, not jq). `CLAUDECODE=1`: 100-row cap + truncated marker. Exit codes from `clicore/cierrors`: 0 ok, 1 generic/network, 2 auth, 3 validation, 4 not found, 5 rate limited, 6 write refused (`write_locked`), 7 async timeout.

## Write safety

The only mutation is the prompt (`prompt push`, `prompt restore`). Order: trim/reject blank, GET live, base-sha check (exit 6), max length, diff, no-op if identical, clicore confirm gate (`--yes` / `--dry-run`), local backup before PATCH, single PATCH never retried, re-GET and verify. Any new write must go through the same confirm gate and a pre-write backup.

## Architecture

```
cmd/zcx/main.go            entry point, version, exit-code dispatch
internal/
  client/                  HTTP client, API methods, models, errors
  commands/                cobra commands, one file per group (root, helpers, sync, stats, prompt, ...)
  config/                  token/chat/base-url resolution, hours.go business hours
  store/                   per-chat cache: conversations/, prompts/, state.json
  syncer/                  incremental sync
  analytics/               KPI computation (stats, escalations)
  promptops/               push/restore safety sequence
  redact/ textdiff/ timeparse/ output/
docs/zipchat-backend-api.md   (local vendor copy, gitignored)
```

## Tests

```sh
go test -race ./...
make build && make lint     # must report 0 issues; golangci-lint must be built with the same Go as the toolchain (if it panics: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
```

HTTP is tested with `httptest` servers; command e2e tests use `internal/commands/e2e_setup_test.go` (captures requests, resets flags between runs). Synthetic fixtures only.

## Commit checklist

1. `gofmt -w .`
2. `go build ./... && go vet ./...`
3. `go test -race ./...`
