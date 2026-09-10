#!/bin/sh
set -eu
image="${1:?Pass the Docker image to test}"
# Default startup must stay unprivileged and include all mount helpers.
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges:true "$image" \
    sh -ec 'test "$(id -u)" = 10001; command -v mount.nfs; command -v mount.cifs; command -v mountpoint; command -v su-exec'
# Even when configured with the full mount permissions, startup drops HTTP/command privileges.
docker run --rm --read-only --user 0:0 --cap-drop ALL \
    --cap-add SYS_ADMIN --cap-add SETUID --cap-add SETGID --cap-add DAC_READ_SEARCH --cap-add DAC_OVERRIDE \
    --security-opt no-new-privileges:true "$image" \
    sh -ec 'test "$(id -u)" = 10001; grep -Eq "^CapEff:[[:space:]]+0+$" /proc/self/status'
# Exercise the real CIFS helper capability setup without contacting a server.
docker run --rm --read-only --user 0:0 --cap-drop ALL \
    --cap-add SYS_ADMIN --cap-add SETUID --cap-add SETGID --cap-add DAC_READ_SEARCH --cap-add DAC_OVERRIDE \
    --security-opt no-new-privileges:true --entrypoint /usr/sbin/mount.cifs "$image" -V
# Invalid configuration must fail before an application process can be started.
if docker run --rm --read-only --cap-drop ALL -e SOURCE_MOUNT=invalid "$image" true; then
    echo 'Invalid mount mode unexpectedly succeeded' >&2
    exit 1
fi
