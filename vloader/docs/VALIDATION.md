# Release validation

## v0.2.3

Reviewed implementation commit: `9d2d9f7` (`docs: prepare v0.2.3 release`), based on `v0.2.2`. This is an author self-review, not an independent review. The review covered the complete English README rewrite, Quick Start accuracy, current mobile-navigation and external-sync documentation, Compose/environment references, and internal Markdown links. No critical or high-severity findings were identified.

Validation completed:

- `git diff --check` completed successfully.
- Verified referenced Compose, environment, security, changelog, and testing-strategy files exist.
- `docker compose config --quiet` completed successfully; the development container is healthy and `/healthz` returned `{"status":"ok"}`.

This release changes documentation only. No application code, Docker image, or external integration behavior was changed, and no production deployment was performed.

## v0.2.2

Reviewed implementation commit: `844d913` (`feat: add external sync and mobile library access`), based on `v0.2.1`. This is an author self-review, not an independent review. The review covered the external sync API authentication and secret persistence, the sole Origin-check exemption, administrator-only root-folder selection, configured Arr target boundaries, request artwork proxying, catalog snapshot preservation, reduced browser catalog responses, and mobile navigation and control structure. No critical or high-severity findings were identified.

Validation completed:

- `go test ./...`, `go test -race ./...`, and `go vet ./...` using Go 1.27.1.
- `govulncheck ./...`: no reachable vulnerabilities.
- Node 24 JavaScript syntax check, `docker compose config --quiet`, and `git diff --check`.
- Docker image build and development-container restart; `/healthz` returned `{"status":"ok"}` and the container reported healthy.
- Verified the served HTML contains the mobile menu and the served CSS/JavaScript contain the responsive library navigation and valid poster control implementation.

Live Radarr, Sonarr, Emby and OIDC integration calls were not exercised during release validation; their request and sync behavior is covered with local mock-server tests. No interactive physical-device browser test or production deployment was performed.

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
