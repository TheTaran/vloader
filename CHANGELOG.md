# Changelog

## Unreleased — 0.1.0
- Initial Go/Docker application with dark cinema-style responsive UI.
- Local/OIDC authentication, protected Emby library synchronization, title filters/details and original-file downloads.
- Read-only source mounts, Docker templates, project and security workflows, regression tests and CI.

- Add Build Docker image with caddymgm-style release tags, GHCR verification and GitHub Releases.
- Add scheduled component version checks for Go, Alpine and pinned Go modules with managed update issues.

- Move Docker development sources into vloader/ and update Compose/Actions paths.
- Integrate read-only NFS/SMB startup support into the main image and consolidate all source options in compose-template.yml.

- Translate the WebGUI, accessibility labels, date formatting and API feedback into English.
