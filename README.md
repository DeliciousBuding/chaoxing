# Chaoxing

[简体中文](README.zh-CN.md)

Chaoxing is a self-hosted, read-only client for courses, assignments, exams, notices, schedules and course materials. One Go binary provides an OAuth-protected HTTP MCP endpoint and local JSON commands.

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
| `query` | Make a direct read-only query with the configured upstream client |
| `configure` | Initialize sealed credentials and a separate owner password hash |
| `version` | Print the build version |

The eight tools are `get_connection_status`, `list_courses`, `list_assignments`, `get_assignment`, `list_exams`, `list_notices`, `get_schedule` and `list_materials`. Core tools never submit assignments, answer exams, check in or change learning progress. Authentication writes session state; read endpoints may use HTTP POST without mutating coursework.

Owner OAuth uses a 60-second authorization code, 15-minute access tokens and 30-day refresh tokens. Tokens are opaque and persisted only as hashed identifiers in private file-backed state, with no database, JWT or JWKS.

Upstream login follows Chaoxing's AES login protocol over HTTPS. On a detected expired session, the GET helper automatically reauthenticates once and retries the read once. Verification requirements remain user actions on the official site. Upstream credentials and persisted cookies use AES-GCM at rest, with a separately stored, root-only key. Encryption cannot protect against root or a process that can read the key. The owner OAuth password is stored as a salted password hash, distinct from the recoverable upstream password.

Cloudflare Tunnel publishes only `127.0.0.1:8792` at an HTTPS origin such as `mcp.example.com`. The private CLI listener `127.0.0.1:8793` is never tunnelled or publicly exposed. See [architecture](docs/architecture.md) and [security policy](SECURITY.md).

## Development and license

Read [CONTRIBUTING.md](CONTRIBUTING.md) for offline checks and contribution boundaries, and [CHANGELOG.md](CHANGELOG.md) for unreleased changes.

This project is licensed under [Apache-2.0](LICENSE). [DanMo661/Xuexitong-mcp](https://github.com/DanMo661/Xuexitong-mcp) and [menghuanshiguang/chaoxing-cli](https://github.com/menghuanshiguang/chaoxing-cli), both MIT-licensed, are protocol references, not copied source code. See [source references](docs/references.md).
