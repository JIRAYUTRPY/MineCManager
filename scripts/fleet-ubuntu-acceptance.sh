#!/usr/bin/env bash
# The hosted Ubuntu job provides an exclusive, disposable systemd VM.
set -euo pipefail
if [[ ${GITHUB_ACTIONS:-} != true || ${RUNNER_ENVIRONMENT:-} != github-hosted ]]; then
  echo "This test creates a temporary system service on a GitHub-hosted runner." >&2
  exit 2
fi
umask 077
workdir=$(mktemp -d "$HOME/mcm-acceptance.XXXXXXXX")
server_id="ci-$(cut -c1-12 /proc/sys/kernel/random/uuid)"
unit="mcm-$server_id.service"
panel_pid=
cleanup() {
  if [[ -n $panel_pid ]]; then kill "$panel_pid" 2>/dev/null || true; fi
  if [[ -s "$workdir/sshd.pid" ]]; then sudo kill "$(cat "$workdir/sshd.pid")" 2>/dev/null || true; fi
  sudo systemctl stop "$unit" 2>/dev/null || true
  sudo rm -f "/run/systemd/system/$unit"
  sudo systemctl daemon-reload
  rm -rf -- "$workdir"
}
trap cleanup EXIT

# Keys and the pinned fingerprint come from this job, independent of the network.
ssh-keygen -q -t ed25519 -N '' -f "$workdir/host"
ssh-keygen -q -t ed25519 -N '' -f "$workdir/client"
ssh-keygen -q -t ed25519 -N '' -f "$workdir/unregistered"
printf 'restrict,from="127.0.0.1" %s\n' "$(cat "$workdir/client.pub")" > "$workdir/authorized_keys"
ssh-keygen -E sha256 -lf "$workdir/host.pub" | awk '{print $2}' > "$workdir/fingerprint"
cat > "$workdir/sshd.conf" <<CONFIG
ListenAddress 127.0.0.1
Port 39222
HostKey $workdir/host
PidFile $workdir/sshd.pid
AuthorizedKeysFile $workdir/authorized_keys
AllowUsers $(id -un)
AuthenticationMethods publickey
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
DisableForwarding yes
PermitTTY no
UsePAM yes
CONFIG
if [[ ! -x /usr/sbin/sshd ]]; then
  sudo apt-get -qq update
  sudo apt-get -y -qq install openssh-server
fi
sudo install -d -m 0755 /run/sshd
sudo /usr/sbin/sshd -t -f "$workdir/sshd.conf"
sudo /usr/sbin/sshd -f "$workdir/sshd.conf"
# The unprivileged cleanup process needs to read this pid in its private directory.
sudo chown "$(id -u)" "$workdir/sshd.pid"

# Exercise the same service lifecycle and world paths as a managed server without
# a vendor download. The bytes below are a fixture, not a Minecraft game session.
game_root="/srv/minecmanager/$server_id"
state=/var/lib/minecmanager
if ! id mcm-game >/dev/null 2>&1; then
  sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin mcm-game
fi
sudo install -d -m 0700 "$state" "$state/servers"
sudo install -d -m 0755 /srv/minecmanager
sudo install -d -m 0750 -o mcm-game "$game_root" "$game_root/world"
printf 'original world\n' > "$workdir/level.dat"
sudo install -m 0640 -o mcm-game "$workdir/level.dat" "$game_root/world/level.dat"
printf '{"server":"%s","path":"%s"}\n' "$server_id" "$game_root" > "$workdir/metadata"
sudo install -m 0600 "$workdir/metadata" "$state/servers/$server_id.json"
cat > "$workdir/unit" <<UNIT
[Unit]
Description=MineCManager acceptance fixture
[Service]
User=mcm-game
ExecStart=/usr/bin/sleep infinity
UNIT
sudo install -m 0644 "$workdir/unit" "/run/systemd/system/$unit"
sudo systemctl daemon-reload
sudo systemctl start "$unit"

export MCM_PANEL_CONFIG="$workdir/data/panel.json"
export MCM_VAULT_KEY_FILE="$workdir/data/vault.key"
export MCM_AUDIT_PATH="$workdir/data/audit.jsonl"
export MCM_FLEET_VAULT="$workdir/data/fleet.vault"
export MCM_PANEL_LISTEN=127.0.0.1:39080
go build -o "$workdir/panel" ./cmd/panel
"$workdir/panel" init
"$workdir/panel" > "$workdir/panel.log" 2>&1 &
panel_pid=$!
curl --silent --show-error --fail --retry 15 --retry-delay 1 --retry-connrefused "http://$MCM_PANEL_LISTEN/fleet" > /dev/null
node scripts/fleet-ubuntu-acceptance.cjs "$workdir" "$server_id"
