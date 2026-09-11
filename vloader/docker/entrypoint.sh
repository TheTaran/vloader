#!/bin/sh
set -eu

fail() { echo "vloader: $*" >&2; exit 1; }
valid_server() {
    case "$1" in ''|*[!a-zA-Z0-9._-]*) fail 'Server must be a hostname or IPv4 address';; esac
    case "$1" in -*) fail 'Invalid server hostname';; esac
}

mode="${SOURCE_MOUNT:-none}"
case "$mode" in
    none) ;;
    nfs|smb)
        [ "$(id -u)" = 0 ] || fail 'Network mounts require the startup user and capabilities from compose-template.yml'
        # Do not cover a configured bind mount or reuse a stale mount silently.
        mountpoint -q /media && fail '/media is already mounted; remove its bind/volume entry from compose.yml'
        case "$mode" in
            nfs)
                valid_server "${NFS_SERVER:-}"
                case "${NFS_EXPORT:-}" in /*) ;; *) fail 'NFS_EXPORT must be an absolute export path';; esac
                echo 'vloader: mounting read-only NFS source'
                timeout 30 mount.nfs "${NFS_SERVER}:${NFS_EXPORT}" /media -n \
                    -o ro,nosuid,nodev,noexec,nfsvers=4,proto=tcp,soft,timeo=100,retrans=2,retry=0 \
                    || fail 'NFS mount failed; verify server, export, host NFS support and mount permissions'
                ;;
            smb)
                valid_server "${SMB_SERVER:-}"
                case "${SMB_SHARE:-}" in ''|*/*|.|..) fail 'SMB_SHARE must name one share';; esac
                [ -n "${SMB_CREDENTIAL_USERNAME:-}" ] || fail 'SMB_CREDENTIAL_USERNAME is required'
                [ -n "${SMB_CREDENTIAL_PASSWORD:-}" ] || fail 'SMB_CREDENTIAL_PASSWORD is required'
                credentials=/tmp/smb-credentials
                umask 077
                smb_username="$SMB_CREDENTIAL_USERNAME"
                smb_domain=""
                case "$smb_username" in
                    *\\*)
                        smb_domain=${smb_username%%\\*}
                        smb_username=${smb_username#*\\}
                        [ -n "$smb_domain" ] || fail 'SMB credential domain is empty'
                        [ -n "$smb_username" ] || fail 'SMB credential username is empty'
                        ;;
                esac
                if [ -n "$smb_domain" ]; then
                    printf 'username=%s\ndomain=%s\npassword=%s\n' "$smb_username" "$smb_domain" "$SMB_CREDENTIAL_PASSWORD" > "$credentials"
                else
                    printf 'username=%s\npassword=%s\n' "$smb_username" "$SMB_CREDENTIAL_PASSWORD" > "$credentials"
                fi
                trap 'rm -f "$credentials"' EXIT
                echo 'vloader: mounting read-only SMB source'
                timeout 30 mount.cifs "//${SMB_SERVER}/${SMB_SHARE}" /media -n \
                    -o "credentials=${credentials},vers=3.1.1,ro,nosuid,nodev,noexec,uid=10001,gid=10001,file_mode=0440,dir_mode=0550" \
                    || fail 'SMB mount failed; verify server, share, credentials, host CIFS support and mount permissions'
                rm -f "$credentials"
                trap - EXIT
                ;;
        esac
        # A failed or missing mount must never fall through to an empty local directory.
        mountpoint -q /media || fail 'Network mount was not established'
        export SOURCE_MODE=mount
        ;;
    *) fail 'SOURCE_MOUNT must be none, nfs or smb';;
esac

# The HTTP process never runs as root, including after privileged mount setup.
if [ "$(id -u)" = 0 ]; then
    exec su-exec 10001:10001 "$@"
fi
exec "$@"
