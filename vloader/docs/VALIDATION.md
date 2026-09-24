# Release validation

## v0.1.10

Reviewed implementation commit: `f385376dc9675b5b7772c93028fd2760c8bcdbfd`.

This was an author self-review, not an independent review. The review covered the admin-only SMTP test route and mail transport, authentication and origin checks, OIDC subject/callback UI, error logging and secret redaction, persistence, the `/data` and `/media` boundary, and Compose mount behavior. No critical or high-severity findings were identified.

Validation completed:

- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `gofmt` using Go 1.27.1.
- `govulncheck ./...`: no vulnerable symbols or reachable vulnerabilities. The scanner reported GO-2026-5932 in the required `golang.org/x/crypto` module's unused `openpgp` package; vloader does not import or call that package, and the scanner reports no affected code.
- Node syntax check for `internal/app/web/app.js`.
- `docker compose config --quiet`, Docker image build, and `vloader/scripts/test-image.sh` including default non-root startup and NFS/SMB helper checks.
- Development container health check returned `{"status":"ok"}`.

Live SMTP delivery, OIDC provider behavior, Emby synchronization, and remote SMB/NFS mounts were not exercised; these require deployment-specific servers and credentials.
