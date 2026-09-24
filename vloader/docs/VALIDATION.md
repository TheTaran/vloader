# Release validation

## v0.1.12

Reviewed implementation commit: `216f76d` (`feat: enrich requests and downloads`), based on `60c179f` (`v0.1.11`). This is an author self-review, not an independent review. The review covered request authentication and ownership, admin-only settings and request moderation, verified OIDC role claims and presentation-only display names, TMDb token redaction and server-side lookup, bounded TMDb requests, HTML escaping of requester/title data, and preservation of the last settings/catalog snapshots on save or sync failure. No critical or high-severity findings were identified.

Validation completed:

- `go test ./...`, `go test -race ./...`, and `go vet ./...` using Go 1.27.1.
- `govulncheck ./...`: zero reachable vulnerabilities; one advisory in a required module package that the application does not call.
- Node 24 JavaScript syntax check, `docker compose config --quiet`, and `git diff --check`.
- Docker image build and `sh vloader/scripts/test-image.sh vloader:dev`, including non-root startup and NFS/SMB helper checks.
- Rebuilt and restarted the development service; `/healthz` returned `{"status":"ok"}` and `/tmdb-logo.svg` returned HTTP 200 (`image/svg+xml`).

Live Emby, Pocket ID, TMDb API and SMTP integrations were not exercised; TMDb API behavior is covered by a mock test. No interactive browser visual review or production deployment was performed.

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
