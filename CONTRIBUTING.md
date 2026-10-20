# Contributing

Coverage improvements should document what was actually enumerated and retain honest completeness markers.

Use Go 1.26.9 or a newer maintained patch in the 1.26 series. CI and releases use `go-version: '1.26.x'` with `check-latest: true`. Before opening a pull request:

```sh
gofmt -l cmd internal
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/chaoxing
go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
govulncheck -scan=package ./...
```

Formatting output should be empty. Race checks require a supported native platform and C compiler. The package-level vulnerability scan checks imported dependency packages, even when vulnerable symbols may not be reachable. CI repeats these checks, and tagged releases depend on them.

Tests must use synthetic fixtures and mocked HTTP boundaries; do not contact a real account in CI. Keep pull requests focused, explain behavior changes and update affected public docs. Include reproduction commands with `mcp.example.com`. Remove credentials, cookies, tokens, phone numbers, student identifiers, private hosts/domains and personal file paths from reports and fixtures.

Actions are pinned to full commit SHAs. When updating one, verify the release tag's resolved commit from the upstream Git repository and update its version comment. Workflow tokens are read-only except the release publication job's `contents: write`. Do not run untrusted pull request code with elevated permissions or secrets.

Use the [private security reporting process](SECURITY.md) for vulnerabilities. Original contributions use the project's Apache-2.0 license; record the license and source of any third-party material.
