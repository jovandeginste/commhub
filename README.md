# CommHub

Local-first communication hub. A daemon that ingests messages from many
platforms — Mattermost, Teams, mail, OpenCode, Smartschool, … — through one
REST API, and stores them in a single SQLite database with a normalized,
queryable schema.

Connectors are separate tools. They fetch from a platform and push to CommHub;
the hub never talks to the platforms itself and never stores credentials.

## Quickstart

```sh
make build
cp config.example.yaml commhub.yaml   # optional
./bin/commhub migrate                 # create the schema + default spheres
./bin/commhub serve                   # http://127.0.0.1:8080
```

Ingest one message:

```sh
curl -s localhost:8080/api/v1/messages -H 'Content-Type: application/json' -d '{
  "source": "mattermost",
  "account": "work",
  "channel": {"external_id": "channel-42", "name": "general", "sphere": "work"},
  "external_id": "post-1001",
  "sender": {"kind": "username", "value": "jdoe", "display_name": "John Doe"},
  "body": "Hello",
  "body_format": "markdown",
  "sent_at": "2026-11-24T09:00:00Z",
  "sphere": "work"
}'
```

Read messages:

```sh
curl -s 'localhost:8080/api/v1/messages?source=mattermost&unread=true&limit=20'
```

Batch or NDJSON ingestion, the entity API and the query parameters are
documented in [docs/design.md](docs/design.md).

## Query from the CLI

`query` and `get` talk to the REST API, not the database:

```sh
./bin/commhub query
./bin/commhub query --source mail --since 2026-01-01 --unread
./bin/commhub query --tag urgent --tag-mode all --json
./bin/commhub query --param has_attachments=false --param language=en
./bin/commhub get 0192...
```

The API URL defaults to the configured `listen` address (wildcard binds become
`127.0.0.1`); the token defaults to `api_token`, or the first `api_tokens`
entry. Override them with `--url` / `--token` or `COMMHUB_API_URL` /
`COMMHUB_API_TOKEN`.

`--json` prints the raw API response for scripting. When another page is
available, the table output prints `next cursor: ...` on stderr; pass it with
`--cursor`.

## Run as a systemd user service

```sh
make install
systemctl --user daemon-reload
systemctl --user enable --now commhub.service
journalctl --user -u commhub -f
```

`make install` puts the binary in `~/.local/bin` and the unit in
`~/.config/systemd/user`. The unit:

- stores the database in `~/.local/share/commhub/commhub.db` (created on first
  start, directory mode `0700`, file mode `0600` via `UMask=0077`);
- reads optional settings from `~/.config/commhub/commhub.yaml`;
- reads optional secrets from `~/.config/commhub/commhub.env`;
- restarts on failure and binds to `127.0.0.1:8080`.

Set an API token without putting it in the unit file:

```sh
mkdir -p ~/.config/commhub
printf 'COMMHUB_API_TOKENS=%s\n' "$(openssl rand -hex 32)" \
  > ~/.config/commhub/commhub.env
chmod 600 ~/.config/commhub/commhub.env
systemctl --user restart commhub
```

To keep the hub running without an active login session:

```sh
loginctl enable-linger "$USER"
```

If you install the binary elsewhere, edit `ExecStart=` in
`contrib/systemd/commhub.service` before running `make install`.

## How it fits together

```
platform ── connector ──POST /api/v1/messages──▶ CommHub ──REST──▶ you
```

- **Natural keys on the wire.** Connectors send source/account/channel slugs and
  platform handles; the hub creates what is missing.
- **Idempotent.** A message is unique per `(account, external_id)` and carries a
  `content_hash`; replaying a sync reports `unchanged` and preserves local state
  (read, starred, tags, sphere).
- **Per-source identities, cross-platform people.** Correspondents group the
  handles (email, username, member id) of one person.
- **Spheres** classify messages as work, personal, hockey, school, … at message
  or channel level.
- **Single, batch and streaming ingestion**, all with per-item results.

## Configuration

| key | env | default |
| --- | --- | --- |
| `database` | `COMMHUB_DATABASE` | `commhub.db` |
| `listen` | `COMMHUB_LISTEN` | `127.0.0.1:8080` |
| `api_tokens` | `COMMHUB_API_TOKENS` | empty (auth disabled) |
| `api_url` | `COMMHUB_API_URL` | derived from `listen` |
| `api_token` | `COMMHUB_API_TOKEN` | first `api_tokens` entry |
| `batch_size` | `COMMHUB_BATCH_SIZE` | `500` |
| `log_level` | `COMMHUB_LOG_LEVEL` | `info` |

`api_tokens` is a comma-separated list of bearer tokens. `api_url` and
`api_token` are used by the `query` and `get` CLI commands.

## Development

```sh
make test     # unit + API tests
make lint     # golangci-lint --allow-parallel-runners
make deps     # tidy + vendor
```

Layout:

```
cmd/commhub/     CLI entrypoint: serve, migrate, ingest, query, get, version
pkg/api/         HTTP API (echo v5)
pkg/client/      HTTP client used by query/get
pkg/ingest/      idempotent write path
pkg/model/       GORM models, enums, migrations
pkg/store/       SQLite connection, migrations, seed data
pkg/config/      viper configuration
contrib/systemd/ user unit and secret environment example
docs/design.md   data model, API contract, design decisions
```

## Status

v1: schema, ingestion, full message query API, CLI client (`query`, `get`).
Planned: connectors, web UI, FTS5 search, auto-classification rules, per-account
sync cursors.
