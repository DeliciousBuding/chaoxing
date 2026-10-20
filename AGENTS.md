# Contributor and agent guidance

Last updated: 2026-10-20 15:10

Chaoxing is a self-hosted course information service with explicitly scoped sign-in, answer-generation and coursework-submission capabilities. It is not a general task-completion bot.

- Protocol parsing and authenticated requests belong in `internal/upstream`, including class activity feeds, course-group sign-in cards, sign-in submission, work-job discovery and work-question parsing.
- Sign-in protocol state, persistent device identity and account identity belong in `internal/upstream`; do not duplicate them in watchers or adapters. Supported submissions are normal, location, and password/gesture when the operator supplies the sign code.
- Transport-independent operation IDs, schemas and validation belong in `internal/operation`; CLI, daemon and MCP must share that registry instead of defining parallel contracts.
- Course-time polling, group/class feed merging, automatic sign-in decisions and per-activity attempt state belong in `internal/watch`; it must not learn HTTP protocol details.
- OAuth scopes are carried through `internal/authz`: reads require `chaoxing.read`; sign-in requires `chaoxing.sign`; local drafts require `chaoxing.work`; answer generation requires `chaoxing.answer`; upstream work save/submit requires `chaoxing.work.submit`; uploads require `chaoxing.upload`; face enrollment/signing requires `chaoxing.face`. Never widen one scope to cover another action class.
- Recoverable credentials and session encryption belong in `internal/vault`.
- Connector authorization belongs in `internal/access`; do not mix it with upstream login.
- `internal/mcphttp` adapts tools to MCP; `cmd/chaoxing` owns process wiring.
- Never publish credentials, real account identifiers, deployment hosts/domains, private paths, or live responses. Use synthetic fixtures and example domains.
- Keep unknown/partial coverage explicit. An empty feed is not proof that every course has no outstanding work.
- Do not follow answer-start links or add arbitrary URL/command tools.
- Slider captcha may use the local image-pair solver, DamaGou, or the bounded manual challenge/answer flow; QR sign-in requires the operator-scanned raw `qr_payload`. Password/gesture sign-in may use an operator-provided sign code only.
- `internal/answer` owns external answer providers and type normalization; `internal/workdraft` validates and persists generated/manual answers. Upstream work changes must remain behind `save_work`/`submit_work` and the submit scope. Final submission requires every question to have a draft.
- `upload_file` reads only a filename directly under that account's private `<state_dir>/uploads` directory; reject path separators and arbitrary paths. Face enrollment and face sign-in require the separate face scope.
- Before a change is complete, run the applicable Go tests/vet; security behavior needs rejection-path tests, not just happy paths.
- Keep product docs in this repository. Installation state and secret recovery details belong to the deployer's private operations documentation.

Architecture: `docs/architecture.md`; contracts: `docs/tools.md`; security: `SECURITY.md`.
