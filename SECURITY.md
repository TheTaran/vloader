# vloader Security Policy

## Reporting
Report vulnerabilities privately to the repository owner. Do not attach credentials, media files or private catalog data to public issues. GitHub private vulnerability reporting may be enabled by the owner; it is not assumed to be configured.

## Trust model
All authenticated vloader accounts have the same administrator privileges, including settings and access to the configured Emby catalog. vloader does not map Emby per-user permissions. Only explicitly trusted users should be allowed. Local admin is configured through .env. OIDC users are allowed by stable subject IDs within one configured issuer.

The Emby API key is a server-side credential. The configured Emby server is an administrator-controlled trusted network destination; private addresses are supported intentionally. Do not expose the GUI to untrusted users. The default port is bound to loopback. Use HTTPS through a trusted reverse proxy for remote use and set APP_URL to its exact origin.

## Controls
- bcrypt password verification; randomly generated initial password, no shipped default password.
- Opaque random sessions with 12-hour expiry, logout invalidation and bounded storage.
- HttpOnly/SameSite cookies, Secure on HTTPS, mutation Origin checks, CSP and frame blocking.
- OIDC signature, issuer, audience, expiry, nonce, browser-bound state, PKCE and subject allowlist.
- Server-side Emby API tokens; redirects rejected to prevent credential forwarding.
- Read-only media roots and os.OpenRoot confinement, including symlink escapes.
- Non-root container, dropped capabilities, read-only root filesystem, bounded logs and resources.
- Atomic mode-0600 settings/catalog snapshots. Secrets remain plaintext on the protected persistent volume and in .env; protect host and backups.

## Operational limits
All sessions are invalidated on restart. Login attempts are globally limited to one per second; protect an Internet-facing instance additionally at the reverse proxy. The catalog is a mirror of configured server data and image bytes are fetched on demand, not an offline image archive. Downloads stream to the browser and do not create server-side download jobs. Emby API streaming requires an available server; mounted file downloads require the configured share. All title IDs are checked against the active catalog.

## Verification
See docs/TESTING-STRATEGY.md. Live provider and share verification requires operator-supplied configuration.
