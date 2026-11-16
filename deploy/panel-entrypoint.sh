#!/bin/sh
set -eu
mkdir -p /data
chown mcm:mcm /data
chmod 700 /data
su-exec mcm /usr/local/bin/mcm-panel init
exec su-exec mcm /usr/local/bin/mcm-panel
