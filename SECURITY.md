# vloader Security Policy

## Reporting
Report vulnerabilities privately to the repository owner. Do not attach credentials, media files or private catalog data to public issues. GitHub private vulnerability reporting may be enabled by the owner; it is not assumed to be configured.

## Trust model
The local account has administrator privileges. OIDC users are admitted by exact stable subject IDs or exact values from a configured ID-token group claim within one configured issuer. Subjects in `OIDC_ADMIN_SUBJECTS` and group members in `OIDC_ADMIN_GROUPS` receive administrator privileges; other allowed subjects and members of `OIDC_ALLOWED_GROUPS` are ordinary users. Administrators can change settings and all authenticated users can access the configured Emby catalog. vloader does not map Emby per-user permissions. Only explicitly trusted users should be allowed.

The Emby API key is a server-side credential. The configured Emby server is an administrator-controlled trusted network destination; private addresses are supported intentionally. Do not expose the GUI to untrusted users. The default port is bound to loopback. Use HTTPS through a trusted reverse proxy for remote use and set APP_URL to its exact origin.

## Controls
- bcrypt password verification; randomly generated initial password, no shipped default password.
- Opaque random sessions with 12-hour expiry, logout invalidation and bounded storage.
- HttpOnly/SameSite cookies, Secure on HTTPS, mutation Origin checks, CSP and frame blocking.
- OIDC signature, issuer, audience, expiry, nonce, browser-bound state, PKCE and exact subject/group allowlists from verified ID-token claims.
- Server-side Emby API tokens; redirects rejected to prevent credential forwarding.
- The external `POST /api/external/sync` webhook accepts only an administrator-generated high-entropy key in `X-Api-Key` or a Bearer header. vloader stores only its SHA-256 hash, displays the raw key once, and supports immediate rotation and revocation. This machine endpoint is the sole mutation exempt from browser Origin checks; its API-key check replaces session and CSRF authentication.
- Read-only media roots and os.OpenRoot confinement, including symlink escapes.
- Non-root container, dropped capabilities, read-only root filesystem, bounded logs and resources.
- Atomic mode-0600 settings/catalog/request snapshots. Emby, OIDC, SMTP, Radarr and Sonarr credentials remain plaintext in the protected persistent volume and are never returned to ordinary users. Protect the host and backups. SMTP notifications use STARTTLS with certificate verification by default. Administrators can explicitly disable TLS for a trusted plain-SMTP relay; in that mode message content and SMTP credentials are transmitted unencrypted.
- Radarr and Sonarr URLs and API keys are administrator-controlled. Approved requests are sent only to these configured URLs over the server-side HTTP client; API keys remain in `/data/settings.json` and are never exposed to users or browser code. Root folders and profiles are selected from the services when an administrator approves a request. Use HTTPS for remote Arr instances and restrict their API access to the vloader host where possible.
- Authenticated request searches are proxied through vloader to the configured Radarr or Sonarr instance. Search responses expose only bounded title metadata and provider IDs; searching never adds content, changes request state or calls an Arr mutation endpoint.
- Request details are fetched only from the configured Radarr or Sonarr service. vloader does not send request titles or provider IDs to TMDb or TVDB. Obsolete metadata credentials and Arr root/profile selections are removed from older settings files during startup.
- Request snapshots contain requester identifiers, verified OIDC email addresses used for availability mail, and display names. Email addresses are omitted from API responses. Protect `/data/wishes.json` and its backups as user data.

## Operational limits
All sessions are invalidated on restart. Login attempts are globally limited to one per second; protect an Internet-facing instance additionally at the reverse proxy. The catalog is a mirror of configured server data and image bytes are fetched on demand, not an offline image archive. Downloads stream to the browser and do not create server-side download jobs. Emby API streaming requires an available server; mounted file downloads require the configured share. All title IDs are checked against the active catalog.

## Verification
See docs/TESTING-STRATEGY.md. Live provider and share verification requires operator-supplied configuration.
