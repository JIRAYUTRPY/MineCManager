#!/usr/bin/env python3
"""Install a MineCManager release and operate the systemd update service (Ubuntu)."""
import argparse
import contextlib
import datetime
import fcntl
import grp
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import platform
import pwd
import re
import secrets
import shlex
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

REPO = "JIRAYUTRPY/MineCManager"
API = "https://api.github.com/repos/" + REPO + "/releases/latest"
BASE = Path("/opt/minecmanager")
ETC = Path("/etc/mcm-managed")
STATE = Path("/var/lib/mcm-release")
PANEL_DATA = Path("/var/lib/mcm-managed-panel")
FLEET_STATE = Path("/var/lib/minecmanager")
UNITS = Path("/etc/systemd/system")
TAG = re.compile(r"v?(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})")
NODE = re.compile(r"[a-z][a-z0-9-]{0,31}")
SOCKET = "/run/mcm-release.sock"
PANEL_URL = "http://127.0.0.1:9080"
MANAGED = "# Managed by MineCManager setup.py\n"


class Problem(Exception):
    def __init__(self, message, status=409):
        super().__init__(message)
        self.status = status


def run(*args, **kwargs):
    return subprocess.run([str(arg) for arg in args], check=True, **kwargs)


def unit(*args):
    run("systemctl", *args)


def active(name):
    return subprocess.run(["systemctl", "is-active", "--quiet", name]).returncode == 0


def atomic(path, data, mode=0o600):
    path = Path(path)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=".mcm-")
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            os.fchmod(stream.fileno(), mode)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(tmp, path)
        syncdir(path.parent)
    finally:
        Path(tmp).unlink(missing_ok=True)


def syncdir(path):
    fd = os.open(path, os.O_DIRECTORY | os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def save(path, obj):
    atomic(path, (json.dumps(obj, indent=2) + "\n").encode())


def read(path, default=None):
    try:
        return json.loads(path.read_bytes())
    except FileNotFoundError:
        if default is not None:
            return default
        raise


@contextlib.contextmanager
def locked(name="operation", wait=False):
    STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (STATE / (name + ".lock")).open("a") as stream:
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | (0 if wait else fcntl.LOCK_NB))
        except BlockingIOError:
            raise Problem("Another maintenance operation is running") from None
        yield


def version(tag):
    match = TAG.fullmatch(tag) if isinstance(tag, str) else None
    if not match:
        raise Problem("Expected a stable release tag such as 1.2.3 or v1.2.3", 400)
    return tuple(map(int, match.groups()))


def installed():
    path = BASE / "current"
    if not path.is_symlink():
        return ""
    tag = path.readlink().name
    version(tag)
    return tag


def request(url, timeout=30):
    return urllib.request.urlopen(urllib.request.Request(url, headers={
        "User-Agent": "MineCManager-release-installer", "Accept": "application/json" if url == API else "*/*",
    }), timeout=timeout)


def latest(role=None):
    try:
        with request(API) as response:
            data = response.read((1 << 20) + 1)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return None
        raise
    if len(data) > 1 << 20:
        raise Problem("Release metadata too large", 502)
    release = json.loads(data)
    if release.get("draft") is not False or release.get("prerelease") is not False:
        raise Problem("GitHub Latest does not identify a published stable release", 502)
    tag = release.get("tag_name")
    try:
        version(tag)
    except Problem:
        raise Problem(f"GitHub Latest tag {tag!r} must be a stable X.Y.Z or vX.Y.Z version", 502) from None
    required = bundle_files(role) if role else ["setup.py"]
    asset_metadata(release, "SHA256SUMS", 65536)
    for name in required:
        asset_metadata(release, name, 128 << 20)
    return release


def asset_metadata(release, name, limit):
    matches = [a for a in release.get("assets", []) if a.get("name") == name and a.get("state") == "uploaded"]
    tag = release["tag_name"]
    if len(matches) != 1:
        raise Problem(f"Release {tag} needs one uploaded {name}. Check the GitHub Release workflow.", 502)
    asset = matches[0]
    digest = asset.get("digest") or ""
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise Problem(f"Release {tag} has no valid SHA-256 digest for {name}", 502)
    size = asset.get("size")
    if type(size) is not int or not 0 < size <= limit:
        raise Problem(f"Release {tag} has an invalid size for {name}", 502)
    return size, digest[7:]


def fetch_asset(release, name, output, expected=None, limit=128 << 20):
    size, digest = asset_metadata(release, name, limit)
    if expected is not None and digest != expected:
        raise Problem("Invalid release checksum: " + name, 502)
    url = "https://github.com/" + REPO + "/releases/download/" + release["tag_name"] + "/" + name
    actual, total = hashlib.sha256(), 0
    with request(url, 90) as response, output.open("xb") as dest:
        while chunk := response.read(1 << 20):
            total += len(chunk)
            if total > size:
                raise Problem("Oversized release asset: " + name, 502)
            dest.write(chunk)
            actual.update(chunk)
        dest.flush()
        os.fsync(dest.fileno())
    if total != size or actual.hexdigest() != digest:
        raise Problem("Checksum mismatch: " + name, 502)


def bundle_files(role):
    arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
    if not arch:
        raise Problem("Supported Linux architectures are amd64 and arm64")
    component = "mcm-panel" if role == "panel" else "mcm-agent"
    files = {component + "-linux-" + arch: component, "setup.py": "setup.py"}
    if role == "game":
        files["MineCManagerBridge.jar"] = "MineCManagerBridge.jar"
    return files


def stage(release, role):
    files = bundle_files(role)
    releases = BASE / "releases"
    releases.mkdir(parents=True, mode=0o755, exist_ok=True)
    dest = releases / release["tag_name"]
    with tempfile.TemporaryDirectory(dir=releases) as scratch:
        folder = Path(scratch) / "bundle"
        folder.mkdir()
        manifest = Path(scratch) / "SHA256SUMS"
        fetch_asset(release, "SHA256SUMS", manifest, limit=65536)
        sums = {}
        for line in manifest.read_text().splitlines():
            m = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
            if not m or m[2] in sums:
                raise Problem("Malformed release manifest", 502)
            sums[m[2]] = m[1]
        for name, local in files.items():
            if name not in sums:
                raise Problem("Missing checksum for " + name, 502)
            if dest.exists():
                with (dest / local).open("rb") as existing:
                    h = hashlib.file_digest(existing, "sha256").hexdigest()
                if h != sums[name]:
                    raise Problem("Cached release differs from published asset: " + name)
            else:
                fetch_asset(release, name, folder / local, sums[name])
                (folder / local).chmod(0o644 if local.endswith(".jar") else 0o755)
        if not dest.exists():
            folder.chmod(0o755)
            syncdir(folder)
            folder.rename(dest)
            syncdir(releases)


def select(tag):
    version(tag)
    tmp = BASE / ".next"
    tmp.unlink(missing_ok=True)
    tmp.symlink_to(Path("releases") / tag)
    os.replace(tmp, BASE / "current")
    syncdir(BASE)


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")


def status():
    state = read(STATE / "status.json", {})
    current = installed()
    candidate = state.get("latest")
    available = bool(current and candidate and version(candidate) > version(current))
    return {**state, "installed": current, "available": available}


def record(**fields):
    state = read(STATE / "status.json", {})
    state.update(fields)
    save(STATE / "status.json", state)
    return state


def check():
    with locked():
        try:
            cfg = read(ETC / "install.json", {})
            release = latest(cfg.get("role"))
            target = release["tag_name"] if release else ""
            record(latest=target, checked=now(), check_error="")
        except Exception as err:
            record(latest="", checked=now(), check_error=str(err))
            raise
    return status()


def queue(tag):
    version(tag)
    with locked():
        state = status()
        if state.get("phase") in ("queued", "applying", "rollback_failed"):
            raise Problem("Maintenance is pending or recovery needs attention")
        if state.get("latest") != tag or version(tag) <= version(state["installed"]):
            raise Problem("Check releases again before applying a newer version")
        record(phase="queued", target=tag, message="", previous=state["installed"])
        try:
            unit("restart", "--no-block", "mcm-release-apply.service")
        except Exception:
            record(phase="failed", message="Could not start the update worker")
            raise
    return status()

def owned_dir(path, user):
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    account = pwd.getpwnam(user)
    os.chown(path, account.pw_uid, account.pw_gid)
    path.chmod(0o700)


def account(name):
    try:
        pwd.getpwnam(name)
    except KeyError:
        run("useradd", "--system", "--user-group", "--no-create-home", "--shell", "/usr/sbin/nologin", name)


def managed_file(path, body):
    if path.exists() and not path.read_text().startswith(MANAGED):
        raise Problem("An unmanaged file is already present: " + str(path))
    atomic(path, (MANAGED + body.lstrip()).encode(), 0o644)


def envfile(name, values):
    if any("\n" in str(v) or "\r" in str(v) for v in values.values()):
        raise Problem("Invalid environment value")
    atomic(ETC / name, "".join(f"{k}={v}\n" for k, v in values.items()).encode())


def sign_certificate(folder, name, usage, address=None):
    ca = ETC / "authority"
    ext = "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=" + usage + "\n"
    if address:
        ext += "subjectAltName=IP:" + address + "\n"
    (folder / "extensions").write_text(ext)
    run("openssl", "req", "-new", "-newkey", "rsa:3072", "-nodes", "-subj", "/CN=" + name,
        "-keyout", folder / "key.pem", "-out", folder / "request.pem", stderr=subprocess.DEVNULL)
    run("openssl", "x509", "-req", "-in", folder / "request.pem", "-CA", ca / "ca.pem",
        "-CAkey", ca / "key.pem", "-set_serial", str(secrets.randbits(150) + 1), "-days", "825",
        "-sha256", "-extfile", folder / "extensions", "-out", folder / "cert.pem", stderr=subprocess.DEVNULL)
    shutil.copyfile(ca / "ca.pem", folder / "ca.pem")
    (folder / "extensions").unlink()
    (folder / "request.pem").unlink()


def tls_access(folder, user):
    gid = grp.getgrnam(user).gr_gid
    os.chown(folder, 0, gid)
    folder.chmod(0o750)
    for name in ("ca.pem", "cert.pem", "key.pem"):
        os.chown(folder / name, 0, gid)
        (folder / name).chmod(0o640)


def panel_tls():
    ca = ETC / "authority"
    if not ca.exists():
        with tempfile.TemporaryDirectory(dir=ETC) as temp:
            part = Path(temp) / "authority"
            part.mkdir(mode=0o700)
            run("openssl", "req", "-x509", "-newkey", "rsa:3072", "-nodes", "-days", "3650",
                "-sha256", "-subj", "/CN=MineCManager private CA", "-addext", "basicConstraints=critical,CA:TRUE",
                "-addext", "keyUsage=critical,keyCertSign,cRLSign", "-keyout", part / "key.pem",
                "-out", part / "ca.pem", stderr=subprocess.DEVNULL)
            part.rename(ca)
    dest = ETC / "panel-tls"
    if not dest.exists():
        with tempfile.TemporaryDirectory(dir=ETC) as temp:
            part = Path(temp) / "client"
            part.mkdir(mode=0o700)
            sign_certificate(part, "mcm-panel", "clientAuth")
            tls_access(part, "mcm-panel")
            part.rename(dest)


def updater_units(user):
    managed_file(UNITS / "mcm-release.socket", f"""
[Unit]
Description=MineCManager local release control
[Socket]
ListenStream={SOCKET}
SocketMode=0660
SocketGroup={user}
Accept=yes
[Install]
WantedBy=sockets.target
""")
    managed_file(UNITS / "mcm-release@.service", f"""
[Service]
ExecStart=/usr/bin/python3 {BASE}/current/setup.py rpc
StandardInput=socket
StandardOutput=socket
StandardError=journal
RuntimeMaxSec=90
""")
    for action in ("apply", "check"):
        boot = "[Install]\nWantedBy=multi-user.target\n" if action == "apply" else ""
        managed_file(UNITS / ("mcm-release-" + action + ".service"), f"""
[Unit]
Description=MineCManager release {action}
After=network-online.target
Wants=network-online.target
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 {BASE}/current/setup.py worker-{action}
TimeoutStartSec=20min
{boot}""")
    managed_file(UNITS / "mcm-release-check.timer", """
[Unit]
Description=Check published MineCManager releases every six hours
[Timer]
OnCalendar=*-*-* 00,06,12,18:00:00
Persistent=true
RandomizedDelaySec=15min
[Install]
WantedBy=timers.target
""")


def service_file(name, extra):
    managed_file(UNITS / (name + ".service"), f"""
[Unit]
Description=MineCManager managed {name}
After=network.target
[Service]
User={name}
Group={name}
{extra}
ExecStart={BASE}/current/{name}
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
UMask=0027
[Install]
WantedBy=multi-user.target
""")


def prepare_panel():
    account("mcm-panel")
    panel_tls()
    owned_dir(PANEL_DATA, "mcm-panel")
    envfile("panel.env", {
        "MCM_PANEL_CONFIG": PANEL_DATA / "panel.json", "MCM_AUDIT_PATH": PANEL_DATA / "audit.jsonl",
        "MCM_FLEET_VAULT": PANEL_DATA / "fleet.vault", "MCM_VAULT_KEY_FILE": PANEL_DATA / "vault.key",
        "MCM_CLIENT_CERT": ETC / "panel-tls/cert.pem", "MCM_CLIENT_KEY": ETC / "panel-tls/key.pem",
        "MCM_CA_CERT": ETC / "panel-tls/ca.pem",
    })
    service_file("mcm-panel", f"""EnvironmentFile={ETC}/panel.env
EnvironmentFile=-{ETC}/nodes.env
EnvironmentFile=-{ETC}/panel-local.env
ExecStartPre={BASE}/current/mcm-panel init
ReadWritePaths={PANEL_DATA}""")


def game_unit(cfg):
    return "mcm-" + cfg["id"] + ".service"


def game_root(cfg):
    return Path("/srv/minecmanager") / cfg["id"]


def bridge(cfg, tag):
    # The game user performs every write into its own writable directory.
    root = game_root(cfg)
    prefix = ["runuser", "-u", "mcm-game", "--"]
    run(*prefix, "mkdir", "-p", root / "plugins")
    src = BASE / "releases" / tag / "MineCManagerBridge.jar"
    dest = root / "plugins/MineCManagerBridge.jar"
    temp = run(*prefix, "mktemp", str(dest) + ".XXXXXXXX", capture_output=True, text=True).stdout.strip()
    try:
        run(*prefix, "install", "-m", "0644", src, temp)
        run(*prefix, "mv", "-fT", temp, dest)
    finally:
        run(*prefix, "rm", "-f", temp)


def prepare_game(cfg, pairing):
    account("mcm-agent")
    state = Path("/var/lib/mcm-managed-agent")
    owned_dir(state, "mcm-agent")
    dest = ETC / "agent-tls"
    dest.mkdir(mode=0o700, exist_ok=True)
    for name in ("ca.pem", "cert.pem", "key.pem"):
        atomic(dest / name, pairing[name].encode())
    tls_access(dest, "mcm-agent")
    paper_env = ETC / "paper.env"
    if not paper_env.exists():
        envfile("paper.env", {"MCM_PAPER_TOKEN": secrets.token_hex(32)})
    host = agent_host(cfg["address"])
    envfile("agent.env", {"MCM_AGENT_TOKEN": pairing["token"], "MCM_AGENT_ADDR": host + ":9081",
                          "MCM_SERVER_ROOT": game_root(cfg), "MCM_SYSTEMD_UNIT": game_unit(cfg),
                          "MCM_STATE_ROOT": state, "MCM_SERVER_CERT": dest / "cert.pem",
                          "MCM_SERVER_KEY": dest / "key.pem", "MCM_CA_CERT": dest / "ca.pem"})
    drop = UNITS / (game_unit(cfg) + ".d")
    drop.mkdir(mode=0o755, exist_ok=True)
    managed_file(drop / "50-mcm-bridge.conf", f"[Service]\nEnvironmentFile={paper_env}\n")
    service_file("mcm-agent", f"""EnvironmentFile={ETC}/agent.env
EnvironmentFile={paper_env}
SupplementaryGroups=mcm-game
ReadWritePaths={state}
ReadWritePaths={game_root(cfg)}""")
    rule = Path("/etc/polkit-1/rules.d/50-mcm-managed-agent.rules")
    rule.parent.mkdir(parents=True, exist_ok=True)
    # JavaScript polkit files use // for comments.
    header = "// Managed by MineCManager setup.py\n"
    body = f"""polkit.addRule(function(action, subject) {{
    if (subject.user === "mcm-agent" && action.id === "org.freedesktop.systemd1.manage-units" &&
        action.lookup("unit") === "{game_unit(cfg)}" &&
        ["start", "stop", "restart"].indexOf(action.lookup("verb")) >= 0) return polkit.Result.YES;
}});
"""
    if rule.exists() and not rule.read_text().startswith(header):
        raise Problem("An unmanaged polkit rule already exists")
    atomic(rule, (header + body).encode(), 0o644)


def agent_host(address):
    addr = ipaddress.ip_address(address)
    return f"[{addr}]" if addr.version == 6 else str(addr)


def health(cfg, running):
    service = "mcm-panel" if cfg["role"] == "panel" else "mcm-agent"
    deadline = time.monotonic() + (150 if running else 30)
    while time.monotonic() < deadline:
        try:
            if not active(service):
                raise OSError("service is starting")
            if cfg["role"] == "panel":
                with urllib.request.urlopen(PANEL_URL + "/fleet", timeout=2) as resp:
                    if resp.status == 200:
                        return
            else:
                with socket.create_connection((cfg["address"], 9081), timeout=2):
                    pass
                if running:
                    token = (ETC / "paper.env").read_text().strip().split("=", 1)[1]
                    req = urllib.request.Request("http://127.0.0.1:9082/v1/players",
                                                 headers={"Authorization": "Bearer " + token})
                    with urllib.request.urlopen(req, timeout=3) as resp:
                        if resp.status != 200:
                            raise OSError("bridge is starting")
                return
        except (OSError, http.client.HTTPException):
            time.sleep(1)
    raise Problem("Health check timed out for " + service, 502)


@contextlib.contextmanager
def fleet_lock(cfg):
    if cfg["role"] != "game":
        yield
        return
    if not (FLEET_STATE / "servers").is_dir():
        raise Problem("Deploy the Paper server using Fleet before pairing")
    with (FLEET_STATE / "control.lock").open("a") as stream:
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise Problem("A Fleet operation is running on the game VM; retry after it completes") from None
        yield


def game_running(cfg):
    if cfg["role"] != "game":
        return False
    state = run("systemctl", "show", "-p", "ActiveState", "--value", game_unit(cfg),
                capture_output=True, text=True).stdout.strip()
    if state not in ("active", "inactive"):
        raise Problem("Game service must be active or inactive before maintenance: " + state)
    return state == "active"


def activate(cfg, tag, running):
    if cfg["role"] == "game":
        unit("stop", "mcm-agent.service")
        unit("stop", game_unit(cfg))
        bridge(cfg, tag)
    select(tag)
    service = "mcm-panel" if cfg["role"] == "panel" else "mcm-agent"
    unit("restart", service)
    if cfg["role"] == "game" and running:
        unit("start", game_unit(cfg))
    health(cfg, running)


def rollback(cfg, state, reason):
    try:
        activate(cfg, state["previous"], state["game_active"])
    except Exception as err:
        record(phase="rollback_failed", message=f"{reason}; recovery failed: {err}")
        raise
    record(phase="failed", message=reason + "; previous release restored")


def apply_update():
    with locked(wait=True):
        state = status()
        if state.get("phase") not in ("queued", "applying"):
            return
        cfg = read(ETC / "install.json")
        if state["phase"] == "applying":
            with fleet_lock(cfg):
                rollback(cfg, state, "Interrupted update")
            return
        try:
            release = latest(cfg["role"])
            target = state["target"]
            if not release or release["tag_name"] != target or version(target) <= version(installed()):
                raise Problem("Selected release is stale; check releases again")
            stage(release, cfg["role"])
            with fleet_lock(cfg):
                state = record(phase="applying", previous=installed(), game_active=game_running(cfg))
                try:
                    activate(cfg, target, state["game_active"])
                except Exception as err:
                    rollback(cfg, state, str(err))
                    raise
                record(phase="succeeded", message="", completed=now())
        except Exception as err:
            if state["phase"] == "queued":
                record(phase="failed", message=str(err))
            raise


def recover():
    with locked():
        state = status()
        if state.get("phase") not in ("applying", "rollback_failed"):
            raise Problem("There is no interrupted rollback to recover")
        cfg = read(ETC / "install.json")
        with fleet_lock(cfg):
            rollback(cfg, state, "Operator recovery")
        return status()


def preflight(cfg):
    service = "mcm-panel" if cfg["role"] == "panel" else "mcm-agent"
    load_state = subprocess.run(["systemctl", "show", "-p", "LoadState", "--value", service + ".service"],
                                capture_output=True, text=True).stdout.strip()
    if load_state != "not-found" or (UNITS / (service + ".service")).exists():
        raise Problem("Existing " + service + " installation detected; see the migration notes")
    port = 9080 if cfg["role"] == "panel" else 9081
    address = "127.0.0.1" if cfg["role"] == "panel" else cfg["address"]
    with socket.socket(socket.AF_INET6 if ":" in address else socket.AF_INET) as s:
        s.bind((address, port))
    if cfg["role"] == "game":
        meta = read(FLEET_STATE / "servers" / (cfg["id"] + ".json"))
        if meta.get("platform") != "paper" or meta.get("version") != "1.21.11" or meta.get("path") != str(game_root(cfg)):
            raise Problem("Expected a Fleet Paper 1.21.11 server with the given ID")
        plugins = game_root(cfg) / "plugins"
        if plugins.exists() and any(p.name.lower().startswith(("minecmanagerbridge", "mcm-paper-bridge"))
                                    and p.suffix.lower() == ".jar" for p in plugins.iterdir()):
            raise Problem("Existing bridge JAR detected; preserve it before migrating")
        with socket.socket() as s:
            s.bind(("127.0.0.1", 9082))


def install(role, pair_path=None):
    pairing = read(pair_path) if pair_path else None
    cfg = {"role": role}
    if pairing:
        node, address = pairing["id"], str(ipaddress.ip_address(pairing["address"]))
        if not NODE.fullmatch(node) or not re.fullmatch(r"[a-f0-9]{64}", pairing["token"]):
            raise Problem("Invalid enrollment file", 400)
        cfg.update(id=node, address=address)
    with locked(), fleet_lock(cfg):
        existing = read(ETC / "install.json", {})
        if existing and existing != cfg:
            raise Problem("This VM already manages a different role or node")
        state = status()
        if existing and state.get("phase") != "installing":
            if not installed():
                raise Problem("Incomplete installation: current release missing")
            if pairing:
                tls = ETC / "agent-tls"
                if any((tls / name).read_text() != pairing[name] for name in ("ca.pem", "cert.pem", "key.pem")):
                    raise Problem("Enrollment differs from the installed node credentials")
                if "MCM_AGENT_TOKEN=" + pairing["token"] not in (ETC / "agent.env").read_text().splitlines():
                    raise Problem("Enrollment token differs from the installed node")
            return state
        release = latest(role)
        if not release:
            raise Problem("No published stable GitHub Release is available")
        if not existing:
            preflight(cfg)
        stage(release, role)
        ETC.mkdir(mode=0o755, parents=True, exist_ok=True)
        if not existing:
            record(phase="installing", game_active=game_running(cfg))
            save(ETC / "install.json", cfg)
        running = status()["game_active"]
        tag = release["tag_name"]
        if role == "panel":
            prepare_panel()
        else:
            prepare_game(cfg, pairing)
        service = "mcm-panel" if role == "panel" else "mcm-agent"
        updater_units(service)
        unit("daemon-reload")
        unit("enable", service + ".service", "mcm-release-apply.service")
        try:
            activate(cfg, tag, running)
        except Exception:
            if running:
                unit("start", game_unit(cfg))
            raise
        unit("enable", "--now", "mcm-release.socket", "mcm-release-check.timer")
        record(phase="installed", latest=tag, checked=now(), check_error="", message="")
        return status()


def pair(args):
    if read(ETC / "install.json")["role"] != "panel" or not NODE.fullmatch(args.node):
        raise Problem("Run pair on the managed Panel with a valid Fleet server ID")
    address = str(ipaddress.ip_address(args.address))
    if ipaddress.ip_address(address).is_unspecified or ipaddress.ip_address(address).is_loopback:
        raise Problem("Use a reachable address on the game VM")
    if args.ssh and not re.fullmatch(r"(?:[a-z_][a-z0-9_-]*@)?[a-zA-Z0-9][a-zA-Z0-9.:-]*", args.ssh):
        raise Problem("Invalid SSH destination; use an OpenSSH host alias for custom ports")
    with locked():
        nodes = ETC / "pairs"
        nodes.mkdir(mode=0o700, exist_ok=True)
        dest = nodes / args.node
        env_name = "MCM_NODE_" + args.node.upper().replace("-", "_") + "_TOKEN"
        registration = {"id": args.node, "url": "https://" + agent_host(address) + ":9081", "token_env": env_name}
        old = next((n for n in read(PANEL_DATA / "panel.json")["nodes"] if n["id"] == args.node), None)
        if old and (old != registration or not dest.exists()):
            raise Problem("Node ID is already registered with different credentials")
        if not dest.exists():
            with tempfile.TemporaryDirectory(dir=nodes) as temp:
                p = Path(temp) / "node"
                p.mkdir(mode=0o700)
                sign_certificate(p, args.node, "serverAuth", address)
                bundle = {n: (p / n).read_text() for n in ("ca.pem", "cert.pem", "key.pem")}
                bundle.update(id=args.node, address=address, token=secrets.token_hex(32))
                save(p / "pair.json", bundle)
                for n in ("ca.pem", "cert.pem", "key.pem"):
                    (p / n).unlink()
                p.rename(dest)
        bundle = read(dest / "pair.json")
        if bundle["address"] != address:
            raise Problem("Existing pairing uses a different address")
        if args.ssh:
            # The destination receives a fixed installer and a private bundle over pinned SSH.
            remote_steps = """set -eu
umask 077
d=$(mktemp -d)
trap 'rm -rf -- "$d"' EXIT
tar -xf - -C "$d"
/usr/bin/python3 "$d/setup.py" game "$d/pair.json"
"""
            remote = "sudo -n /bin/sh -c " + shlex.quote(remote_steps)
            import tarfile
            with tempfile.TemporaryFile() as stream:
                with tarfile.open(fileobj=stream, mode="w") as archive:
                    archive.add(BASE / "current/setup.py", arcname="setup.py", recursive=False)
                    archive.add(dest / "pair.json", arcname="pair.json", recursive=False)
                stream.seek(0)
                run("ssh", "-o", "StrictHostKeyChecking=yes", "-o", "BatchMode=yes", "--",
                    args.ssh, remote, stdin=stream)
        values = {}
        for path in sorted(nodes.glob("*/pair.json")):
            entry = read(path)
            values["MCM_NODE_" + entry["id"].upper().replace("-", "_") + "_TOKEN"] = entry["token"]
        data = "".join(f"{k}={v}\n" for k, v in values.items()).encode()
        env = ETC / "nodes.env"
        if not env.exists() or env.read_bytes() != data or not old:
            atomic(env, data)
            unit("restart", "mcm-panel")
            health({"role": "panel"}, False)
        if not old:
            key = Path(args.admin_key_file).read_text().strip()
            req = urllib.request.Request(PANEL_URL + "/v1/nodes", data=json.dumps(registration).encode(),
                                         headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
            with urllib.request.urlopen(req, timeout=10) as response:
                if response.status != 201:
                    raise Problem("Panel did not register node")
        return {"node": args.node, "enrollment_file": str(dest / "pair.json"), "agent_url": registration["url"]}


def rpc():
    try:
        line = sys.stdin.buffer.readline(1025)
        if len(line) > 1024:
            raise Problem("Request too large", 400)
        q = json.loads(line)
        action = q.get("action") if isinstance(q, dict) else None
        if action == "status":
            result = status()
        elif action == "check":
            result = check()
        elif action == "apply":
            result = queue(q.get("version"))
        else:
            raise Problem("Unknown action", 400)
        reply = {"code": 202 if action == "apply" else 200, "data": result}
    except Exception as err:
        reply = {"code": getattr(err, "status", 502), "error": str(err)}
    print(json.dumps(reply), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("panel", help="install the Panel on this VM")
    game = commands.add_parser("game", help="install Agent and bridge on the existing Fleet Paper VM")
    game.add_argument("enrollment", type=Path)
    link = commands.add_parser("pair", help="provision mTLS and register the game VM in the Panel")
    link.add_argument("node")
    link.add_argument("address")
    link.add_argument("--ssh", help="also install on this verified SSH destination using passwordless sudo")
    link.add_argument("--admin-key-file", default=str(PANEL_DATA / "admin-key.txt"))
    for cmd in ("status", "check", "recover", "rpc", "worker-apply", "worker-check"):
        commands.add_parser(cmd)
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error("Run on the target Ubuntu systemd VM as root (sudo)")
    os.umask(0o022)
    if args.command == "rpc":
        rpc()
        return
    if args.command == "panel":
        result = install("panel")
    elif args.command == "game":
        result = install("game", args.enrollment)
    elif args.command == "pair":
        result = pair(args)
    elif args.command in ("check", "worker-check"):
        result = check()
    elif args.command == "recover":
        result = recover()
    elif args.command == "worker-apply":
        apply_update()
        return
    else:
        result = status()
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (Problem, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        sys.exit(str(error))
