# Changelog

## v0.1.11
- Allow OIDC access and administrator roles by verified group claims, including Pocket ID `groups`.
- Add group claim, allowed groups and administrator groups to Settings → Authentication.
- Fix authentication settings saves so they preserve and do not revalidate unrelated Emby connection settings.
- Add an admin-controlled SMTP TLS disable option for trusted plain-SMTP relays, with an explicit in-UI security warning.

## v0.1.10
- Display the configured OIDC callback URL in Settings → Authentication.
- Log failed HTTP requests and relevant persistence, Emby, OIDC and transfer errors to Docker stdout/stderr without recording request query strings or media paths.
- Add an admin-only SMTP test email action to Settings → Email notifications.
- Keep the read-only SMB/NFS media root fixed at `/media`, separate from persistent application state in `/data`.
- Clarify that OIDC allowed subject IDs are `sub` claim values, not scopes or email addresses.

## v0.1.9
- Initial Go/Docker application with dark cinema-style responsive UI.
- Local/OIDC authentication, protected Emby library synchronization, title filters/details and original-file downloads.
- Read-only source mounts, Docker templates, project and security workflows, regression tests and CI.

- Add Build Docker image with caddymgm-style release tags, GHCR verification and GitHub Releases.
- Add scheduled component version checks for Go, Alpine and pinned Go modules with managed update issues.

- Move Docker development sources into vloader/ and update Compose/Actions paths.
- Integrate read-only NFS/SMB startup support into the main image and consolidate all source options in compose-template.yml.

- Translate the WebGUI, accessibility labels, date formatting and API feedback into English.
- Reduce the left navigation text size and add admin-only SMTP email notifications for new title requests.
