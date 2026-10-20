# Security policy

Security fixes target the current default branch and latest release. Older releases do not have a separate maintenance commitment.

Report vulnerabilities using [GitHub private vulnerability reporting](https://github.com/DeliciousBuding/chaoxing/security/advisories/new). Include the affected version, impact and a minimal synthetic reproduction. If the repository has not enabled private reporting, open an issue containing only a request for a private contact channel; do not disclose vulnerability details there. Never attach credentials, cookies, tokens, private hostnames, account identifiers or real coursework.

## Trust model

The public MCP listener requires owner OAuth authorization. Its password is stored as a salted PBKDF2-SHA256 hash and is distinct from the upstream password. The upstream password and cookie state are encrypted with AES-GCM because unattended login needs recoverable credentials. Keep the encryption key separately stored and root-only, with equivalently restrictive ACLs on Windows. Root or a compromised service process with key access can decrypt the vault.

Authorization codes expire in 60 seconds, access tokens in 15 minutes and refresh families in 30 days. Access tokens are random opaque values; refresh proofs authenticate issuer-bound family/counter/nonce values. Only active token digests and bounded family state are persisted, not raw tokens. This database-free design uses neither JWT nor JWKS.

Publish only loopback port 8792 through an HTTPS tunnel. Port 8793 is a private CLI listener protected by a local bearer token; never expose it through a tunnel, public bind or port forward. Protect configuration, OAuth state, watch configuration and backups as private data.

The upstream core reads coursework and can submit only the explicitly allowlisted normal, location, password and gesture sign-in operations. Authentication, device identity, watch state and local persistence still write service state; sign-in additionally writes upstream attendance state. Password and gesture sign-in require the operator-provided code and verify it before submission. Automatic AES login and a single expired-session reauthentication do not bypass official verification requirements. Face, photo and QR sign-in remain unsupported.
Slider captcha has a bounded manual flow: the service stores `enc2`, captcha keys and validation tokens only in memory, returns image URLs but never raw challenge tokens, expires challenges after 10 minutes, and never solves or blind-retries them automatically. Face, photo and QR sign-in remain unsupported.

`chaoxing.work` can edit only the local answer-draft store. It cannot submit upstream work, execute commands, read arbitrary files or choose arbitrary URLs. Draft state is validated, bounded and written atomically with private file permissions. Exported request payloads may contain upstream form tokens; treat them like session data and do not paste them into public issues.

Course-group activity discovery is disabled unless `group_activities` is enabled. It obtains a transient Easemob token from the account identity response, keeps that token only in memory, reads bounded group history, and parses only course sign-in cards. It does not send group messages or expose general chat history.

Additional tenants have separate OAuth state, keys, credentials, cookie jars and caches. Startup rejects overlapping state paths and key aliases, including existing symlink ancestors and hard-linked key files. The same trusted process and egress transports still serve every tenant; endpoint health and outbound capacity are shared. This is not a sandbox against the operator, host root, process compromise or shared-egress observation. See [account and resource boundaries](docs/architecture.md).

Incomplete global assignment coverage is a known correctness limit pending a global coverage fix. Per-course `complete: true` is conditional on signed work-list pagination and count agreement for every matched class; it does not establish account-wide completeness. Do not use the global feed as proof that an account has no pending work. Checksums in releases detect corruption; verify the repository and release source as well.
