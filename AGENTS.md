# Contributor and agent guidance

Last updated: 2026-10-20 15:10

Chaoxing is a self-hosted course information service with an explicitly scoped sign-in capability. It is not a general task-completion bot.

- Protocol parsing and authenticated requests belong in `internal/upstream`, including class activity feeds, course-group sign-in cards, sign-in submission, work-job discovery and work-question parsing.
- Sign-in protocol state, persistent device identity and account identity belong in `internal/upstream`; do not duplicate them in watchers or adapters. Supported submissions are normal, location, and password/gesture when the operator supplies the sign code.
- Transport-independent operation IDs, schemas and validation belong in `internal/operation`; CLI, daemon and MCP must share that registry instead of defining parallel contracts.
- Course-time polling, group/class feed merging, automatic sign-in decisions and per-activity attempt state belong in `internal/watch`; it must not learn HTTP protocol details.
- OAuth scopes are carried through `internal/authz`; reads require `chaoxing.read`, sign-in requires `chaoxing.sign`, and local answer-draft edits require `chaoxing.work`. Future edit tools must add a distinct scope instead of widening read.
- Recoverable credentials and session encryption belong in `internal/vault`.
- Connector authorization belongs in `internal/access`; do not mix it with upstream login.
- `internal/mcphttp` adapts tools to MCP; `cmd/chaoxing` owns process wiring.
- Never publish credentials, real account identifiers, deployment hosts/domains, private paths, or live responses. Use synthetic fixtures and example domains.
- Keep unknown/partial coverage explicit. An empty feed is not proof that every course has no outstanding work.
- Do not follow answer-start links or add arbitrary URL/command tools.
- Do not implement automatic captcha solving, face or photo bypasses. Slider captcha may expose a bounded manual challenge/answer flow; QR sign-in requires the operator-scanned raw `qr_payload`. Face and photo return `UNSUPPORTED` until an explicit contract exists. Password/gesture sign-in may use an operator-provided sign code only.
- Manual answering is local-only in `internal/workdraft`: read questions, persist validated drafts, and export save/submit payloads. Do not add automatic answer generation or upstream work submission without a separate explicit contract.
- Before a change is complete, run the applicable Go tests/vet; security behavior needs rejection-path tests, not just happy paths.
- Keep product docs in this repository. Installation state and secret recovery details belong to the deployer's private operations documentation.

Architecture: `docs/architecture.md`; contracts: `docs/tools.md`; security: `SECURITY.md`.
