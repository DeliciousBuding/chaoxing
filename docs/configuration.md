# Configuration and tunnel setup

Build the single binary with the Go version in `go.mod`. `CHAOXING_CONFIG` selects the private config file; its default is `/etc/chaoxing/config.json`. `configure` refuses to replace an existing configuration implicitly.

## Initialize private state

```sh
chaoxing configure --state-dir /var/lib/chaoxing --key-file /etc/chaoxing/secret.key --issuer https://mcp.example.com
```

Supply a JSON object on stdin using a trusted private input mechanism. Required fields are `phone`, `password` (upstream login) and `owner_password` (a distinct owner OAuth credential, at least 16 characters). Do not type actual credentials into command arguments, shell history, issue reports, examples or tracked files. No working credentials are shipped in this repository.

The generated config records `state_dir`, `key_path`, `issuer`, `listen`, `local_listen`, `local_token` and `owner_password_hash`. Treat the whole config as private because it includes the local bearer token. The state directory holds sealed upstream credentials, sealed cookie state and owner OAuth state. Keep the encryption key separately from state backups; a backup containing both can be decrypted by whoever reads it.

On Linux, use a root-owned private state directory (0700), config and state files (0600), and a separate root-only key (0600). The code creates private files but does not establish your OS account ownership or fix permissions of an existing key. Verify them before starting. On Windows, apply equivalent restrictive ACLs; Unix mode bits alone do not provide that boundary.

Run `chaoxing serve` under the trusted service identity. Keep one daemon per state directory. The local identity running `chaoxing call` must have permission to read the private configuration; do not grant untrusted clients access to the vault key.

## Publish only the MCP listener

Default listeners are `127.0.0.1:8792` (public MCP and OAuth behind the tunnel) and `127.0.0.1:8793` (private CLI). Both stay bound to loopback. The issuer must match the public HTTPS origin.

Use this ingress fragment in your own private Cloudflare Tunnel configuration:

```yaml
ingress:
  - hostname: mcp.example.com
    service: http://127.0.0.1:8792
  - service: http_status:404
```

Keep tunnel credentials outside this repository. Never add an ingress rule or port forward for 8793, and never expose both listeners. Configure the MCP client with `https://mcp.example.com/mcp`, then complete owner login and consent for `chaoxing.read`.

Dynamic client registration (DCR) is enabled by default. CIMD is opt-in with `CHAOXING_ENABLE_CIMD=true`; enable it only after verifying that the host can fetch HTTPS metadata from `chatgpt.com`. Discovery advertises the selected capability truthfully. DCR does not require fetching that external metadata.

OAuth state v2 uses a separate `oauth-refresh.key` to authenticate refresh proofs. Keep it with the private OAuth state when backing up; losing or replacing it invalidates refresh families. Pre-release v1 state is not migrated silently. Stop the service, preserve a recovery copy, and explicitly reauthorize clients when initializing v2.

Before use, verify that listeners are loopback-only, 8793 has no public route, and an unauthenticated MCP request cannot call tools.
