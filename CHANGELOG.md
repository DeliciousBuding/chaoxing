# Changelog

## Unreleased

- Public English and Chinese product documentation, tool contracts, configuration and security boundaries.
- Documented verified per-course signed work-list pagination and conditional count-agreement coverage, while retaining the global feed's incomplete status.
- Offline tests for upstream parsing, encrypted state and authorization rejection paths.
- CI for Go tests, race detection, vet, formatting, build, dependency vulnerability scanning.
- CI and release toolchains resolve the latest Go 1.26 patch; build documentation requires Go 1.26.9 or newer in that series.
- Tag-triggered Linux arm64/amd64 and Windows amd64 release builds with SHA256 checksums.

### Known limitations

Global assignment feed coverage remains incomplete; account-wide coverage is pending a global coverage fix. Per-course completeness is scoped to matched classes and conditional on upstream count agreement. Notices and materials also have incomplete coverage. The current 30-second MCP callback context can constrain multi-class scans; a 60-second context is under consideration.
