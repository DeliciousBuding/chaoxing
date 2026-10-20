# Chaoxing

[![CI](https://github.com/DeliciousBuding/chaoxing/actions/workflows/ci.yml/badge.svg)](https://github.com/DeliciousBuding/chaoxing/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/DeliciousBuding/chaoxing)](https://github.com/DeliciousBuding/chaoxing/releases)

[简体中文](README.zh-CN.md)

Chaoxing is an agent-first, self-hosted control plane for courses, assignments, exams, notices, schedules, course materials, class sign-in and manual answer preparation. One Go binary provides an OAuth-protected HTTP MCP endpoint and an authenticated local JSON API. Agents can discover capabilities, read course state, submit explicitly scoped sign-ins, and edit local answer drafts without receiving arbitrary URL or command execution powers.

## Agent-first control plane

The service is designed to be operated remotely by an agent, not merely called as a library:

- MCP clients connect to `/mcp` through an HTTPS tunnel and receive the same validated tool catalog as the local CLI.
- The local loopback API on port 8793 accepts one JSON tool request with a bearer token and is suitable for host-local automation.
- `get_capabilities` and `schema <operation>` expose operation IDs, scopes, input schemas and output schemas before any account data is touched.
- Every adapter uses `internal/operation` as the single contract; MCP, local API and direct query cannot define parallel argument or result formats.
- Account operations are queued and bounded, upstream URLs are allowlisted, and callers cannot supply arbitrary URLs, shells or filesystem paths.

| OAuth scope | Capability |
| --- | --- |
| `chaoxing.read` | Course, assignment, exam, notice, schedule, material, chapter, activity and work-question reads |
| `chaoxing.sign` | Normal, location, and operator-coded password/gesture sign-in |
| `chaoxing.work` | Create, update, list, delete and export local answer drafts |

There is intentionally no generic `write` scope. Refresh tokens can narrow scopes but never widen them. The upstream password and cookie vault use AES-GCM with a separately stored key; the owner OAuth password is stored only as a salted PBKDF2 hash. See [security policy](SECURITY.md).

The global assignment feed remains incomplete. A course-name query scans matching classes through the signed work list and can report `complete: true` when pagination agrees with upstream totals. That verifies the matched classes, not the whole account. An empty global pending list does not prove that every assignment is submitted. See [coverage and tool contracts](docs/tools.md).

## Build and use

Use Go 1.26.9 or a newer maintained patch in the 1.26 series, as required by `go.mod`, then build:

```sh
go build -o chaoxing ./cmd/chaoxing
./chaoxing version
```

Tagged releases build Linux arm64, Linux amd64 and Windows amd64 binaries with `SHA256SUMS`. Release checksums detect corruption; they are not a separate signature. Rename the chosen binary to `chaoxing` (or `chaoxing.exe`), and on Linux grant execute permission before installation.

Configure the server using private JSON on stdin, then run `chaoxing serve`. [Configuration and tunnel setup](docs/configuration.md) describes the inputs, file permissions and listener boundary. Do not put credentials in command arguments or public files.

With a configured, running local service, send a tool request on stdin:

```sh
printf '%s\n' '{"tool":"get_connection_status","arguments":{}}' | chaoxing call
```

## Interfaces and authentication

| Command | Purpose |
| --- | --- |
| `serve` | Own upstream session and caches; serve public MCP and private loopback calls |
| `call` | Send one JSON request to the running daemon's private listener |
| `query` | Make a direct bounded query with the configured upstream client |
| `configure` | Initialize sealed credentials and a separate owner password hash |
| `version` | Print the build version |

Use `chaoxing capabilities` for the verified tool catalog and unsupported domains, and `chaoxing schema assignments.list` for the input/output contract. In addition to the original eight tools, bounded study overview, source-backed action items, a bounded study digest and capability discovery are available. Authentication writes session state; read endpoints may use HTTP POST without mutating coursework.

An optional activity watcher can poll selected classes during their course times, merge sign-in cards published in course-group messages, send operator alerts, and automatically submit normal, location, and password/gesture sign-ins when the matching class has a preset sign code. Per-class preset coordinates, persistent device identity, interval jitter and one-attempt state are built in. If upstream returns a slider-captcha challenge, the watcher alerts once; an agent can complete the bounded `get_sign_captcha` -> `answer_sign_captcha` -> `sign_activity` flow manually. Photo, QR and face requirements stay alert-only.

Notifications can be sent simultaneously to Feishu webhooks, ntfy and Gotify. Watch state is atomic, private and separate from upstream credentials. Each tenant account can run its own watcher with independent classes, schedules, coordinates, sign codes, state and notification destinations.

Manual answering is deliberately local-first:

```text
list_work_jobs -> get_work_questions -> upsert_work_draft -> export_work_draft
```

The draft store validates question IDs against the current upstream work page, persists answers atomically, and exports both `pyFlag=1` save and final submit request payloads. The service does not generate answers or post those payloads upstream.

```sh
chaoxing assignments list --course-id <id-from-list-courses> --status pending --json
chaoxing assignments get <id-from-list-assignments> --json
chaoxing study overview --course-id <id-from-list-courses> --json
chaoxing study digest --course-id <id-from-list-courses> --json
chaoxing schema assignments.list
```

Business commands use the running daemon. JSON is the default: successful data goes to stdout, complete failures to stderr, and partial source coverage returns usable stdout with exit code 3. Check `data.meta.coverage` before claiming a source is empty or exhaustive. See [tool contracts](docs/tools.md).

Owner OAuth uses a 60-second authorization code, 15-minute random opaque access tokens and 30-day refresh families. Refresh proofs authenticate the family, counter and nonce, keeping rotation state bounded while recognizing replay. `chaoxing.read` grants course reads; `chaoxing.sign` is required for sign-in; `chaoxing.work` is required for local answer-draft edits. Raw access/refresh tokens are never persisted; the authorization server stays file-backed with no JWT or JWKS. An embedded SQLite store is used only for optional egress-proxy health. DCR is enabled by default; CIMD is an explicit opt-in. Optional SOCKS5 proxy pooling and isolated multi-tenant serving under `/t/<id>/` are documented in [configuration](docs/configuration.md); both default off.

Upstream login follows Chaoxing's AES login protocol over HTTPS. On a detected expired session, the GET helper automatically reauthenticates once and retries the read once. Verification requirements remain user actions on the official site. Upstream credentials and persisted cookies use AES-GCM at rest, with a separately stored, root-only key. Encryption cannot protect against root or a process that can read the key. The owner OAuth password is stored as a salted password hash, distinct from the recoverable upstream password.

Cloudflare Tunnel publishes only `127.0.0.1:8792` at an HTTPS origin such as `mcp.example.com`. The private CLI listener `127.0.0.1:8793` is never tunnelled or publicly exposed. See [architecture](docs/architecture.md) and [security policy](SECURITY.md).

## Development and license

Read [CONTRIBUTING.md](CONTRIBUTING.md) for offline checks and contribution boundaries, and [CHANGELOG.md](CHANGELOG.md) for unreleased changes.

This project is licensed under [Apache-2.0](LICENSE). [DanMo661/Xuexitong-mcp](https://github.com/DanMo661/Xuexitong-mcp) and [menghuanshiguang/chaoxing-cli](https://github.com/menghuanshiguang/chaoxing-cli), both MIT-licensed, are protocol references, not copied source code. See [source references](docs/references.md).
