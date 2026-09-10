#!/bin/sh
set -eu
# Run on the Docker host with sudo. Arguments are never evaluated as shell code.
# Usage: sudo scripts/mount-smb.sh //server/share /mnt/vloader-media /etc/vloader-smb.credentials
[ "$#" -eq 3 ] || { echo 'Usage: mount-smb.sh //server/share /absolute/mountpoint /absolute/credentials-file' >&2; exit 2; }
case "$1" in //*) ;; *) echo 'An SMB //server/share is required' >&2; exit 2;; esac
case "$2" in /*) ;; *) echo 'Mountpoint must be absolute' >&2; exit 2;; esac
case "$3" in /*) ;; *) echo 'Credentials path must be absolute' >&2; exit 2;; esac
case "$3" in *,*) echo 'Credentials path cannot contain commas' >&2; exit 2;; esac
[ -f "$3" ] || { echo 'Credentials file missing' >&2; exit 2; }
[ "$(stat -c %a "$3")" = 600 ] || { echo 'Credentials file must have mode 600' >&2; exit 2; }
mkdir -p -- "$2"
mount -t cifs "$1" "$2" -o "credentials=$3,vers=3.1.1,ro,nosuid,nodev,noexec,uid=10001,gid=10001,file_mode=0440,dir_mode=0550"
