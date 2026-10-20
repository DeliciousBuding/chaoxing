# Contributor and agent guidance

Last updated: 2026-10-20 13:50

Chaoxing is a self-hosted course information service with an explicitly scoped sign-in capability. It is not a general task-completion bot.

- Protocol parsing and authenticated requests belong in `internal/upstream`.
- Normal/location sign-in protocol state, persistent device identity and account identity belong in `internal/upstream`; do not duplicate them in watchers or adapters.
- Transport-independent operation IDs, schemas and validation belong in `internal/operation`; CLI, daemon and MCP must share that registry instead of defining parallel contracts.
- Course-time polling, automatic sign-in decisions and per-activity attempt state belong in `internal/watch`; it must not learn HTTP protocol details.
- OAuth scopes are carried through `internal/authz`; read tools require `chaoxing.read`, sign-in requires `chaoxing.sign`, and future edit tools must add a distinct scope instead of widening read.
- Recoverable credentials and session encryption belong in `internal/vault`.
- Connector authorization belongs in `internal/access`; do not mix it with upstream login.
- `internal/mcphttp` adapts tools to MCP; `cmd/chaoxing` owns process wiring.
- Never publish credentials, real account identifiers, deployment hosts/domains, private paths, or live responses. Use synthetic fixtures and example domains.
- Keep unknown/partial coverage explicit. An empty feed is not proof that every course has no outstanding work.
- Do not follow answer-start links or add arbitrary URL/command tools.
- Do not implement captcha, face, photo, QR, gesture or password bypasses. Return `UNSUPPORTED` and alert the operator when upstream requires manual verification.
- Before a change is complete, run the applicable Go tests/vet; security behavior needs rejection-path tests, not just happy paths.
- Keep product docs in this repository. Installation state and secret recovery details belong to the deployer's private operations documentation.

Architecture: `docs/architecture.md`; contracts: `docs/tools.md`; security: `SECURITY.md`.
