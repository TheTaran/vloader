# vloader — Project Instructions

vloader is a Go service with an embedded web GUI for mirroring Emby libraries and downloading original media through Emby or read-only NFS/SMB storage.

## Sources and precedence
Adapted from pedrofuentes/agents-template v0.25.0 (commit f5fbbe3c07eb049debf2d60b967dd9612c51d266), and the local caddymgm project instructions and security workflows. See docs/TEMPLATE-SOURCES.md. User instructions take precedence. Templates are customized to this project; Caddy configuration and historical Caddy findings do not apply.

## Workflow
- Read the request, identify affected files with rg, and implement the authorized scope without repetitive approval requests.
- Keep development under /docker/vloader. Never modify caddymgm or a separate production host as part of vloader development.
- Build and test related edits together; rebuild and restart the vloader development container before handing off.
- Use feature branches for subsequent work. Do not force-push or overwrite releases.
- Repository creation and initial upload requested in the bootstrap task are authorized. Later release tags, publishing or production changes need explicit user authorization.
- Never commit .env, media, persisted settings/catalog, credentials, logs, or backups. AGENTS.md and security documentation ARE tracked for this project.
- Preserve data and other applications. Cleanup only identified disposable vloader artifacts; never prune persistent volumes.

## Quality gates
- Add focused tests for behavior changes and security boundaries; reproduce bugs before fixes when practical.
- Run gofmt, go test ./..., go test -race ./..., go vet ./..., and govulncheck ./... for security-sensitive releases.
- Validate Docker Compose, build the image, restart the development service, verify health and relevant logs.
- Document actual test results, limitations and remaining integrations; never claim live Emby/OIDC/NFS/SMB testing without those systems.
- Follow docs/SENTINEL.md for pre-release review. No fabricated reviewer or coverage claims.

## Implementation boundaries
- All catalog, images, settings and media endpoints require authentication.
- Use bcrypt for local credentials; state, nonce, PKCE, issuer/audience/signature verification and explicit sub allowlists for OIDC.
- Use HttpOnly, SameSite cookies, Secure on HTTPS, bounded sessions and origin checks for all mutations.
- Never expose API keys to the browser. Never follow redirects with Emby credentials.
- Treat configured Emby as a trusted administrative endpoint; ordinary remote request input cannot select proxy targets.
- Use os.OpenRoot to contain source downloads and deny nonregular files. Source mounts are read-only.
- Never add Docker socket access, privileged mode, shell-based mounting in HTTP handlers, or global host cleanup.
- Preserve original library IDs and item metadata. Persist catalog snapshots atomically and retain the last successful snapshot on synchronization failure.

## Commands
```sh
go test ./...
go test -race ./...
go vet ./...
docker compose config --quiet
docker compose up -d --build
docker compose ps
```
Go tooling can run in the pinned build image when Go is not installed on the host. Consult README.md for exact commands.

## Documentation
Read Security.md and securityskills.md for security-sensitive changes; update CHANGELOG.md for visible behavior, DECISIONS.md for architecture, and LEARNINGS.md for verified discoveries.
