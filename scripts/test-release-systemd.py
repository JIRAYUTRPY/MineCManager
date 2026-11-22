#!/usr/bin/env python3
"""Run only on an exclusive GitHub-hosted Ubuntu runner. Network source is a release fixture."""
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request

if os.geteuid() != 0 or os.environ.get("RUNNER_ENVIRONMENT") != "github-hosted":
    sys.exit("Needs an exclusive GitHub-hosted runner and systemd")
DIST = Path(sys.argv[1]).resolve()
ROLE = sys.argv[2]
ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("installer", ROOT / "setup.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
if m.ETC.exists() or m.BASE.exists():
    sys.exit("Managed installation already exists")
active_tag = "v1.0.0"
payloads = {}
metadata = {}


class Releases(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/latest":
            b = json.dumps(metadata[active_tag]).encode()
        else:
            b = payloads[self.path]
        self.send_response(200)
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def log_message(self, *args):
        pass


def fixture(root_url, tag, broken=False):
    files = {p.name: p.read_bytes() for p in DIST.iterdir() if p.name != "SHA256SUMS"}
    src = files["setup.py"].decode()
    src = src.replace('API = "https://api.github.com/repos/" + REPO + "/releases/latest"', f'API = "{root_url}/latest"')
    src = src.replace('"https://github.com/" + REPO + "/releases/download/"', repr(root_url + "/assets/"))
    files["setup.py"] = src.encode()
    if broken:
        files["mcm-panel-linux-amd64"] = b"#!/bin/sh\nexit 1\n"
        files["mcm-agent-linux-amd64"] = b"#!/bin/sh\nexit 1\n"
    digest = lambda b: hashlib.sha256(b).hexdigest()
    files["SHA256SUMS"] = "".join(digest(b) + "  " + name + "\n" for name, b in files.items()).encode()
    assets = []
    for name, data in files.items():
        assets.append(dict(name=name, size=len(data), state="uploaded", digest="sha256:" + digest(data)))
        payloads["/assets/" + tag + "/" + name] = data
    metadata[tag] = dict(tag_name=tag, draft=False, prerelease=False, assets=assets)
    return files["setup.py"]


def system(*args):
    return subprocess.run(["systemctl", *args], check=True, capture_output=True, text=True).stdout.strip()


def fetch(url, token, body=None, context=None):
    req = urllib.request.Request(url, data=None if body is None else json.dumps(body).encode(),
                                 headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=15, context=context) as response:
        return response.status, json.load(response)


def wait_phase(url, token, phase, context=None):
    deadline = time.monotonic() + 140
    while time.monotonic() < deadline:
        try:
            _, result = fetch(url, token, context=context)
            if result["phase"] == phase:
                return result
            if result["phase"] == "rollback_failed" or phase == "succeeded" and result["phase"] == "failed":
                raise AssertionError(result)
        except OSError:
            pass
        time.sleep(1)
    raise AssertionError("Timed out waiting for " + phase)


with tempfile.TemporaryDirectory() as tmp, http.server.ThreadingHTTPServer(("127.0.0.1", 0), Releases) as server:
    threading.Thread(target=server.serve_forever, daemon=True).start()
    root_url = "http://127.0.0.1:" + str(server.server_port)
    tmp = Path(tmp)
    bootstrap = tmp / "setup.py"
    bootstrap.write_bytes(fixture(root_url, active_tag))
    fixture(root_url, "v1.1.0")
    fixture(root_url, "v1.2.0", broken=True)
    ctx = None
    if ROLE == "panel":
        subprocess.run([sys.executable, bootstrap, "panel"], check=True)
        token = (m.PANEL_DATA / "admin-key.txt").read_text().strip()
        url = m.PANEL_URL + "/v1/fleet/updates"
        before = (m.PANEL_DATA / "vault.key").read_bytes()
        proc = subprocess.run([sys.executable, bootstrap, "pair", "survival", "10.10.1.4"], check=True, capture_output=True)
        enrollment = json.loads(proc.stdout)
        assert not (Path(enrollment["enrollment_file"]).stat().st_mode & 0o077)
        assert fetch(m.PANEL_URL + "/v1/fleet/update-targets", token)[1] == ["survival"]
        try:
            fetch(url, "x" * 40, {"action": "check"})
            raise AssertionError("Anonymous check accepted")
        except urllib.error.HTTPError as err:
            assert err.code == 403
        args = ["panel"]
    else:
        m.account("mcm-game")
        m.account("mcm-panel")
        m.ETC.mkdir()
        m.panel_tls()
        ca_dir = tmp / "server"
        ca_dir.mkdir()
        address = "127.0.0.2"
        m.sign_certificate(ca_dir, "survival", "serverAuth", address)
        token = "1" * 64
        bundle = {n: (ca_dir / n).read_text() for n in ("cert.pem", "key.pem", "ca.pem")}
        m.save(tmp / "pair.json", bundle | dict(id="survival", address=address, token=token))
        game = Path("/srv/minecmanager/survival")
        game.mkdir(parents=True)
        shutil.chown(game, "mcm-game", "mcm-game")
        (game / "world-sentinel").write_text("world")
        registry = Path("/var/lib/minecmanager/servers")
        registry.mkdir(parents=True, exist_ok=True)
        m.save(registry / "survival.json", dict(platform="paper", version="1.21.11", path=str(game)))
        game_program = Path("/opt/mcm-fixture-game.py")
        game_program.write_text("""import http.server, os, time
if "MCM_PAPER_TOKEN" not in os.environ:
    time.sleep(3600)
class Bridge(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        ok = self.path == "/v1/players" and self.headers.get("Authorization") == "Bearer " + os.environ["MCM_PAPER_TOKEN"]
        self.send_response(200 if ok else 403)
        self.end_headers()
        self.wfile.write(b"[]")
http.server.HTTPServer(("127.0.0.1", 9082), Bridge).serve_forever()
""")
        (m.UNITS / "mcm-survival.service").write_text("[Service]\nUser=mcm-game\nExecStart=/usr/bin/python3 " + str(game_program) + "\n")
        system("daemon-reload")
        system("start", "mcm-survival")
        args = ["game", str(tmp / "pair.json")]
        subprocess.run([sys.executable, bootstrap, *args], check=True)
        ctx = ssl.create_default_context(cafile=str(m.ETC / "authority/ca.pem"))
        ctx.load_cert_chain(m.ETC / "panel-tls/cert.pem", m.ETC / "panel-tls/key.pem")
        url = "https://" + address + ":9081/v1/updates"
        assert (game / "plugins/MineCManagerBridge.jar").read_bytes() == (DIST / "MineCManagerBridge.jar").read_bytes()
    assert fetch(url, token, context=ctx)[1]["installed"] == "v1.0.0"
    subprocess.run([sys.executable, bootstrap, *args], check=True)
    active_tag = "v1.1.0"
    assert fetch(url, token, {"action": "check"}, ctx)[1]["available"]
    assert fetch(url, token, {"action": "apply", "version": active_tag}, ctx)[0] == 202
    assert wait_phase(url, token, "succeeded", ctx)["installed"] == active_tag
    if ROLE == "game":
        assert system("is-active", "mcm-survival") == "active"
        system("stop", "mcm-survival")
    active_tag = "v1.2.0"
    assert fetch(url, token, {"action": "check"}, ctx)[1]["latest"] == active_tag
    assert fetch(url, token, {"action": "apply", "version": active_tag}, ctx)[0] == 202
    assert wait_phase(url, token, "failed", ctx)["installed"] == "v1.1.0"
    if ROLE == "game":
        assert not m.active("mcm-survival")
        assert (game / "world-sentinel").read_text() == "world"
    else:
        assert before == (m.PANEL_DATA / "vault.key").read_bytes()
        assert fetch(m.PANEL_URL + "/v1/fleet/update-targets", token)[1] == ["survival"]
    print(ROLE + " installation, update and failed-health rollback passed")
