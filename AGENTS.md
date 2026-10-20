# Contributor and agent guidance

Last updated: 2026-10-14

Chaoxing is a self-hosted read-only information service, not a task-completion bot.

- Protocol parsing and authenticated requests belong in `internal/upstream`.
- Transport-independent operation IDs, schemas and validation belong in `internal/operation`; CLI, daemon and MCP must share that registry instead of defining parallel contracts.
- Recoverable credentials and session encryption belong in `internal/vault`.
- Connector authorization belongs in `internal/access`; do not mix it with upstream login.
- `internal/mcphttp` adapts tools to MCP; `cmd/chaoxing` owns process wiring.
- Never publish credentials, real account identifiers, deployment hosts/domains, private paths, or live responses. Use synthetic fixtures and example domains.
- Keep unknown/partial coverage explicit. An empty feed is not proof that every course has no outstanding work.
- Do not follow answer-start links or add arbitrary URL/command tools.
- Before a change is complete, run the applicable Go tests/vet; security behavior needs rejection-path tests, not just happy paths.
- Keep product docs in this repository. Installation state and secret recovery details belong to the deployer's private operations documentation.

Architecture: `docs/architecture.md`; contracts: `docs/tools.md`; security: `SECURITY.md`.
