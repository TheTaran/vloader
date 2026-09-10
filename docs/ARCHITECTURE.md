# Architecture

- cmd/vloader: process entrypoint.
- internal/app: HTTP routing, local/OIDC authentication, settings, Emby synchronization and download transport.
- internal/app/web: embedded HTML, CSS and JavaScript; no CDN or frontend build required.
- /data: private settings.json and catalog.json, atomic replacement.
- /media: read-only media root from bind mount, NFS or SMB.

The browser calls only vloader. vloader authenticates all catalog, settings, image and download calls. Emby libraries are copied with source IDs; items retain original ParentId and gain LibraryId for catalog filtering. Failed synchronization leaves the prior snapshot intact. Image requests use the same authenticated API proxy. Downloads stream either from Emby or through an os.Root-contained file handle, with file-range support for mounted sources.

Settings updates and sync are serialized. Readers use a mutex for snapshot publication. Catalogs are bound to the source URL so changing servers cannot serve old IDs against a new server.
