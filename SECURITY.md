# Security policy

Security fixes target the current default branch and latest release. Older releases do not have a separate maintenance commitment.

Report vulnerabilities using [GitHub private vulnerability reporting](https://github.com/DeliciousBuding/chaoxing/security/advisories/new). Include the affected version, impact and a minimal synthetic reproduction. If the repository has not enabled private reporting, open an issue containing only a request for a private contact channel; do not disclose vulnerability details there. Never attach credentials, cookies, tokens, private hostnames, account identifiers or real coursework.

## Trust model

The public MCP listener requires owner OAuth authorization. Its password is stored as a salted PBKDF2-SHA256 hash and is distinct from the upstream password. The upstream password and cookie state are encrypted with AES-GCM because unattended login needs recoverable credentials. Keep the encryption key separately stored and root-only, with equivalently restrictive ACLs on Windows. Root or a compromised service process with key access can decrypt the vault.

Authorization codes expire in 60 seconds, access tokens in 15 minutes and refresh tokens in 30 days. Tokens are opaque and stored only as SHA-256 identifiers in private file-backed state. This database-free design uses neither JWT nor JWKS.

Publish only loopback port 8792 through an HTTPS tunnel. Port 8793 is a private CLI listener protected by a local bearer token; never expose it through a tunnel, public bind or port forward. Protect configuration, OAuth state and backups as private data.

The upstream core is read-only with respect to coursework. Authentication and local persistence still write session state. Automatic AES login and a single expired-session reauthentication do not bypass official verification requirements.

Incomplete global assignment coverage is a known correctness limit pending a global coverage fix. Per-course `complete: true` is conditional on signed work-list pagination and count agreement for every matched class; it does not establish account-wide completeness. Do not use the global feed as proof that an account has no pending work. Checksums in releases detect corruption; verify the repository and release source as well.
