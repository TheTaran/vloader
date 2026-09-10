# Decisions

1. Go net/http with embedded assets keeps the Docker image compact and the deployment self-contained.
2. Docker-hosted runtime supports LAN Emby and network filesystems. Cloudflare Sites cannot host this Go/NFS/SMB runtime.
3. Snapshot JSON provides simple atomic persistence for one service instance; no database migration is needed.
4. Network shares are mounted by Docker or the host, never by a privileged web process.
5. One trusted administrator role is shared by local and allowed OIDC subjects. Per-user Emby ACL mapping is outside this initial scope.
6. Downloads go to the browser; metadata is mirrored locally, images are served from Emby on demand.
