# Chaoxing management UI

This directory contains the owner-facing React management console. It uses:

- React 19 + TypeScript
- Vite 7
- Tailwind CSS 4
- shadcn/ui-style component primitives in `src/components/ui`
- Lucide icons

The production build is emitted to `../internal/webui/dist` and embedded by Go's `embed` package. Production hosts do not need Node.

```sh
npm install
npm run dev     # local development server
npm run build   # type-check and emit the embedded production bundle
```

The UI is intentionally API-driven and metadata-driven. It does not create a second tool contract: every operation displayed or executed comes from `/api/tools`, which in turn uses the Go `internal/operation` allowlist.

Security invariants:

- owner login uses the existing PBKDF2 password hash;
- session and CSRF cookies use `__Host-`, `Secure`, and `SameSite=Strict`;
- sessions are bound to the login client address and expire after eight hours absolutely or thirty minutes idle;
- all mutating requests carry `X-Chaoxing-CSRF`;
- the static bundle has a strict CSP;
- the bundle is same-origin only and contains no analytics or third-party tracker;
- no arbitrary URL, file, or command capability is exposed to the browser.
