# Bootstrap validation — 2026-09-10

## Passed locally
- Go 1.27.1: go test ./... and go vet ./....
- go test -race -cover ./...: passed; internal/app statement coverage 75.1% at the authentication test checkpoint. The later JSON error Content-Type adjustment was rechecked with the full ordinary suite and vet.
- OIDC fixture: allowed subject, denied subject, nonce mismatch, wrong audience, expired token, missing browser state cookie, replay rejection and PKCE parameters.
- Authentication, origin checks, session expiry/logout and protected routes.
- Emby fixture: pagination, token forwarding only to configured source, redirect denial, metadata preservation, atomic failed sync, source switching and downloads.
- Filesystem: source-prefix boundary, traversal, symlink escape, regular download and byte ranges.
- govulncheck v1.8.0: zero reachable vulnerabilities and zero vulnerable imported packages; one advisory exists only in an unused module package.
- JavaScript syntax check using Node 24.
- Base, NFS and SMB Compose configurations; shell syntax for SMB mounting helper.
- Docker image build and container recreation; healthy, non-root service at 127.0.0.1:8090.
- HTTP smoke checks: /, CSS, JS and health 200; anonymous catalog 401; actual local login 200; redacted settings; authenticated catalog 200; logout 200 and subsequent catalog 401.

## Limitations
No real Emby, OIDC provider, NFS export or SMB share was supplied. Fixtures verify the integration contracts but do not certify a particular server configuration. No interactive browser/visual QA was requested or performed. This is author validation, not an independent Sentinel verdict. Image bytes are served on demand from Emby, not mirrored for offline use. No production deployment or release tag was created.

The initial Go 1.26.5 scan found standard-library vulnerabilities. The build was updated to Go 1.27.1 and rescanned successfully. Persistent data and other projects were preserved; only specifically identified obsolete development artifacts may be removed.
