# Chaoxing

[![CI](https://github.com/DeliciousBuding/chaoxing/actions/workflows/ci.yml/badge.svg)](https://github.com/DeliciousBuding/chaoxing/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/DeliciousBuding/chaoxing)](https://github.com/DeliciousBuding/chaoxing/releases)

[简体中文](README.zh-CN.md)

Chaoxing is a self-hosted client for courses, assignments, exams, notices, schedules, course materials and scoped class sign-in. One Go binary provides an OAuth-protected HTTP MCP endpoint and local JSON commands.

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

An optional activity watcher can poll selected classes during their course times, send operator alerts, and automatically submit normal or location sign-ins. Per-class preset coordinates, persistent device identity, interval jitter and one-attempt state are built in; manual-verification sign-ins stay alert-only.

```sh
chaoxing assignments list --course-id <id-from-list-courses> --status pending --json
chaoxing assignments get <id-from-list-assignments> --json
chaoxing study overview --course-id <id-from-list-courses> --json
chaoxing study digest --course-id <id-from-list-courses> --json
chaoxing schema assignments.list
```

Business commands use the running daemon. JSON is the default: successful data goes to stdout, complete failures to stderr, and partial source coverage returns usable stdout with exit code 3. Check `data.meta.coverage` before claiming a source is empty or exhaustive. See [tool contracts](docs/tools.md).

Owner OAuth uses a 60-second authorization code, 15-minute random opaque access tokens and 30-day refresh families. Refresh proofs authenticate the family, counter and nonce, keeping rotation state bounded while recognizing replay. `chaoxing.read` grants course reads; `chaoxing.sign` is required for remote sign-in. Raw access/refresh tokens are never persisted; the authorization server stays file-backed with no JWT or JWKS. An embedded SQLite store is used only for optional egress-proxy health. DCR is enabled by default; CIMD is an explicit opt-in. Optional SOCKS5 proxy pooling and isolated multi-tenant serving under `/t/<id>/` are documented in [configuration](docs/configuration.md); both default off.

Upstream login follows Chaoxing's AES login protocol over HTTPS. On a detected expired session, the GET helper automatically reauthenticates once and retries the read once. Verification requirements remain user actions on the official site. Upstream credentials and persisted cookies use AES-GCM at rest, with a separately stored, root-only key. Encryption cannot protect against root or a process that can read the key. The owner OAuth password is stored as a salted password hash, distinct from the recoverable upstream password.

Cloudflare Tunnel publishes only `127.0.0.1:8792` at an HTTPS origin such as `mcp.example.com`. The private CLI listener `127.0.0.1:8793` is never tunnelled or publicly exposed. See [architecture](docs/architecture.md) and [security policy](SECURITY.md).

## Development and license

Read [CONTRIBUTING.md](CONTRIBUTING.md) for offline checks and contribution boundaries, and [CHANGELOG.md](CHANGELOG.md) for unreleased changes.

This project is licensed under [Apache-2.0](LICENSE). [DanMo661/Xuexitong-mcp](https://github.com/DanMo661/Xuexitong-mcp) and [menghuanshiguang/chaoxing-cli](https://github.com/menghuanshiguang/chaoxing-cli), both MIT-licensed, are protocol references, not copied source code. See [source references](docs/references.md).
