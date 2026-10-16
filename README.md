# zcx

Go CLI for the **Zipchat Backend API** (Zipchat is an AI shopping and support assistant for e-commerce). Single binary, dual-audience output: tables for humans on a TTY, clean JSON for pipes and LLM agents. Built on [`clicore`](https://github.com/nicolasacchi/clicore), a fleet sibling of `pmx`, `ddx`, `gx`.

zcx is read-mostly. Its only write is the master prompt (`prompt push` / `prompt restore`), behind several safety gates.

## Install

```sh
make install     # go install -> $GOBIN / ~/go/bin/zcx
make build       # ./bin/zcx
```

## Authentication and configuration

Resolution order (flag > env > file):

| Setting | Order |
|---------|-------|
| Token (`zc-` one chat, `zcu-` user) | `--token` > `ZIPCHAT_API_TOKEN` > `~/.config/zipchat/env` (`ZIPCHAT_API_TOKEN=...`) > `~/.config/zcx/config.toml` |
| Chat id | `--chat` > `ZCX_CHAT_ID` > `config.toml` (`chat_id`) > auto-detected when `GET /chats` returns exactly one chat |
| Base URL | `ZCX_BASE_URL` > `config.toml` (`base_url`) > `https://app.zipchat.ai/api/integrations/backend_api/v1` |
| Data dir | `$XDG_DATA_HOME/zcx` or `~/.local/share/zcx` (one subdir per chat) |

The token is never printed (masked in `config show`/`doctor`). Run `zcx config doctor` to check the setup.

Optional `~/.config/zcx/config.toml`:

```toml
chat_id = 12345

[scope]                      # default scope for stats/escalations (optional)
host = "www.example-shop.com"          # only conversations started on this host
live_since = "2026-10-14T08:30:00Z"    # go-live floor applied by --live

[hours]                      # business hours; default Mon-Fri 09:00-19:00 Europe/Rome
timezone = "Europe/Rome"
mon = ["09:00-19:00"]
sat = ["09:00-13:00", "14:00-18:00"]   # several intervals per day allowed
sun = []                               # explicitly closed
holidays = ["2026-11-01"]              # closed all day
```

As soon as one weekday key is present, only the listed days are open. All human-facing times are Europe/Rome; the API speaks UTC.

## Global flags

`--chat`, `--token`, `--json`, `--jq <gjson path>`, `--limit N` (0 = unbounded), `--offline` (cache only, no network), `-q/--quiet`, `-v/--verbose` (URLs redacted), `--yes`/`--confirm`, `--dry-run`.

## Commands

```sh
zcx chats list                                   # discover chat ids
zcx config show                                  # resolved config, token masked
zcx config doctor                                # token source, chat id, reachability, rate-limit headers, data dir perms

zcx conversations list --since 7d --all          # also --updated-since; newest activity first
zcx conversations get 123456                     # all message pages
zcx users list [--show-pii]                      # emails masked unless --show-pii
zcx tools list ; zcx tools get <id>              # custom tools
zcx variables list                               # names and has_value only, never values

zcx sync [--full] [--concurrency N]              # incremental local cache (parallel fetch, default 4, max 8)
zcx stats --from 2026-10-14 --to 2026-10-16      # KPIs for a date range
zcx stats --as-of 2026-10-15 --live --offline    # reproducible past snapshot
zcx escalations --waiting                        # escalated, no operator reply yet
zcx export transcripts --from 7d --format jsonl --out t.jsonl
zcx audit                                        # prompt length vs max, tools, variables, rating and lead coverage

zcx prompt get [--raw]
zcx prompt pull -o prompt.txt                    # saves file + local backup, prints sha256
zcx prompt diff prompt.txt
zcx prompt push prompt.txt --base-sha <sha> [--dry-run] [--yes]
zcx prompt history
zcx prompt restore <backup> --base-sha <sha> [--dry-run] [--yes]
zcx version
```

Notes:

- `stats` / `escalations` / `export transcripts` run an incremental sync first unless `--offline`; `--strict-sync` makes a failing pre-sync fatal instead of falling back to cached data.
- `stats` and `escalations` scope: `--channel` (default `chat,ai_search`, instagram excluded), `--host` (default `[scope] host` from config.toml, empty = every host; `--host '*'` overrides the config), `--live` (only conversations created at or after `[scope] live_since`; refused when it is not configured). `--from/--to` accept Rome local dates/times, RFC3339 or relative values like `7d`; a bare `--to`/`--as-of` date includes that whole Rome day.
- Message roles: customer = `user`; bot = `assistant` without `manual_reply`; operator = `assistant` with `manual_reply`.
- `--as-of` ignores messages and escalation/resolution timestamps after the instant. Zipchat stores only the LAST escalation time, so a conversation re-escalated after `--as-of` loses its earlier escalation (counted in `escalations_unknown_as_of`, warned on stderr).
- `export transcripts` redacts emails, phones, codice fiscale, IBAN, card numbers and addresses (best effort) by default; `--mask-orders` also masks order numbers; `--raw` disables redaction and warns on stderr. `--out` writes mode 0600.

## Output modes

- TTY: table. Pipe, `--json` or `--jq`: JSON.
- `--jq` uses **gjson** syntax, not jq (`#.id`, `#.{a:A}`).
- `CLAUDECODE=1` (agent mode): JSON plus a 100-row cap and a `{"truncated":true,"shown":N,"total":M}` marker; `--limit 0` for all.
- Progress goes to stderr.

## Exit codes (clicore canonical)

| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | generic / network |
| 2 | auth failed / forbidden (401, 403, missing token) |
| 3 | validation (422, bad args) |
| 4 | not found (404) |
| 5 | rate limited (429) |
| 6 | write refused (`write_locked`: missing `--yes`, base-sha mismatch) |
| 7 | async timeout |

## Local cache and PII warning

`zcx sync` stores full conversations (all messages), `state.json` and prompt backups under `<data dir>/<chat id>/` (`conversations/`, `prompts/`). **The cache contains customer PII** (names, emails, phones, free-text messages). Directories are 0700, files 0600, writes are atomic. Do not copy the cache into repositories, tickets or chats; use `export transcripts` (redacted) for sharing.

Sync takes the union of conversations from `updated_at_since` and `last_message_at_since` (5 minute overlap from the stored high-water marks), fetches details only when new or changed, and honors `X-RateLimit-Remaining/Reset`. `--full` ignores the marks.

Details are downloaded by a worker pool: `--concurrency N` (default 4, clamped to 1..8; `sync_concurrency = N` in `~/.config/zcx/config.toml` sets it for `sync` and for the automatic pre-sync of `stats`/`escalations`/`export`; the flag wins). The API allows 120 requests/minute per token; the client reserves a quota slot before every request, so parallel workers never exceed the window, and when only 3 requests are left all workers wait for `X-RateLimit-Reset` (a "rate limit nearly exhausted" line on stderr). A 429 is retried for GET only; `prompt push` (PATCH) is never retried. High-water marks advance only when every conversation was fetched and saved; Ctrl-C stops the workers and leaves the marks untouched. If several fetches fail, the error of the lowest conversation id is reported.

## Prompt push safety sequence

`prompt push FILE --base-sha SHA` replaces the WHOLE prompt (no ETag on the API: last write wins). In order:

1. Read FILE, trim like the server, reject blank.
2. GET the live prompt.
3. Refuse (exit 6) if `sha256(live) != --base-sha` (someone edited in the dashboard): re-pull and diff.
4. Refuse if longer than `master_prompt_max_length`.
5. Print the unified diff; if identical, no-op and exit 0.
6. Confirm gate: `--dry-run` previews, without `--yes` refuse with exit 6.
7. Back up the live prompt locally BEFORE the PATCH.
8. Single PATCH, never retried after a network error.
9. GET again and verify the stored prompt equals the trimmed new one; otherwise exit non-zero and report the backup path.

`prompt restore` applies the same guards using a local backup as source.

## Rate limits

120 requests / 60 s per credential (`X-RateLimit-Limit/Remaining/Reset`); 429 when exceeded (exit 5). Clean 429/5xx on reads are retried; PATCH is never retried after a network error.

## What the API cannot do

No attributed sales or revenue, no knowledge base access, no dashboard metrics (CSAT dashboards, usage charts). Stats are computed locally from conversations. Variable values are never returned. Only the prompt is writable through zcx; the API documents other writes that zcx does not implement. See the [Backend API documentation](https://docs.zipchat.ai/installation-and-setup/chat-settings/integrations/backend-api).

## Development

```sh
make build && make test && make lint
```

See `CLAUDE.md` for conventions.
