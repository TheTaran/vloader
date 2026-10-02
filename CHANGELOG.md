## Unreleased

## v0.2.1
- Refresh requests immediately after library sync and match exact titles when Emby omits the requested provider ID, while rejecting conflicting IDs.

## v0.2
- Show complete movie and series posters in request search results, proxied through authenticated vloader endpoints with a strict image-host allowlist.

## v0.1.15
- Add optional Radarr/Sonarr request automation and user-facing title search. Selecting a result creates a pending vloader request; only an administrator approval submits it using the first root folder and profiles returned by the service, while failed or unconfigured integrations leave it pending.
- Use Radarr and Sonarr exclusively for the user request search and request details; remove manual provider entry and TMDb API lookups from the request interface.
- Remove TVDB request artwork, credentials, settings access and attribution from the active application; Radarr and Sonarr now provide all pre-Emby request details.
- Remove obsolete TMDb/TVDB credentials and Radarr/Sonarr root/profile selections from existing settings files during startup.
- Use Emby's `DateCreated` descending order for every library and for the latest dashboard instead of sorting library cards by production year.
- Allow administrators to open the request search and submit test requests while keeping the complete user-request list above the form.

## v0.1.14
- Make All content a latest-updates dashboard for movies and series, newest first; configure its 1–365 day window in Settings (default 14 days).
- Email requesters when an Emby sync first finds their movie or series; use a verified OIDC email claim and prevent duplicate notices on later syncs.

## v0.1.13
- Show wide Emby artwork or TVDB banners on movie and series request cards; configure and store the TVDB API key and optional subscriber PIN in Metadata settings.
- Show TMDb credits once at the bottom of the page and reduce the logo size by 50%.

# Changelog

## v0.1.12
- Enrich IMDb/TMDb requests with cached TMDb posters and title details using an admin-configured API token; bundle and credit the TMDb logo locally.
- Show OIDC `display_name` or `name` in the header and admin request list while keeping authorization tied to stable subjects.
- Add an individual Download button for every episode and use a consistent compact size for all download buttons.
- Link the sidebar version label to GitHub's latest release and mark available updates.
- Refine poster and request-list layouts, including a smaller poster display and the admin-first request list.

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
