#!/usr/bin/env python3
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("installer", Path(__file__).resolve().parents[1] / "setup.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

spec = importlib.util.spec_from_file_location("publisher", Path(__file__).with_name("publish-release.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        root = Path(work.name)
        for name in ("STATE", "ETC", "BASE", "FLEET_STATE"):
            p = root / name
            p.mkdir()
            self.enterContext(patch.object(installer, name, p))
        (installer.FLEET_STATE / "servers").mkdir()
        self.enterContext(patch.object(installer.platform, "machine", return_value="x86_64"))

    def fixture(self, tag):
        files = {"setup.py": b"script", "mcm-panel-linux-amd64": b"binary"}
        digest = lambda data: hashlib.sha256(data).hexdigest()
        files["SHA256SUMS"] = "".join(digest(v) + "  " + k + "\n" for k, v in files.items()).encode()
        release = {"tag_name": tag, "draft": False, "prerelease": False, "assets": [
            {"name": k, "state": "uploaded", "size": len(v), "digest": "sha256:" + digest(v)} for k, v in files.items()]}
        return release, files

    def test_published_stable_only(self):
        release, _ = self.fixture("v1.2.3")
        for change in ({"tag_name": "0.01.0"}, {"tag_name": "1.2.3-rc1"}, {"draft": True}, {"prerelease": True}, {"tag_name": "v1.2.3-beta"},
                       {"tag_name": "main"}, {"tag_name": "../v1.2.3"}):
            with self.subTest(change=change), patch.object(installer, "request", return_value=io.BytesIO(json.dumps(release | change).encode())):
                with self.assertRaises(installer.Problem):
                    installer.latest()
        self.assertGreater(installer.version("v1.10.0"), installer.version("v1.9.9"))

    def test_bare_tag_and_prefixed_tag_have_the_same_version(self):
        release, files = self.fixture("0.0.2")
        self.assertEqual(installer.version("v0.0.2"), installer.version("0.0.2"))
        installer.select("v0.0.1")
        installer.save(installer.ETC / "install.json", {"role": "panel"})

        def request(url, *_):
            if url == installer.API:
                return io.BytesIO(json.dumps(release).encode())
            self.assertIn("/releases/download/0.0.2/", url)
            return io.BytesIO(files[url.rsplit("/", 1)[1]])

        with patch.object(installer, "request", side_effect=request), patch.object(installer, "unit"):
            self.assertTrue(installer.check()["available"])
            self.assertEqual(installer.queue("0.0.2")["target"], "0.0.2")
            installer.stage(release, "panel")
            installer.select("v0.0.2")
            self.assertFalse(installer.check()["available"])
        self.assertTrue((installer.BASE / "releases/0.0.2/mcm-panel").is_file())

    def test_source_only_or_partial_release_is_not_offered(self):
        release, _ = self.fixture("0.0.1")
        installer.select("v0.0.0")
        installer.save(installer.ETC / "install.json", {"role": "panel"})
        for assets, missing in (([], "SHA256SUMS"), ([a for a in release["assets"] if a["name"] != "mcm-panel-linux-amd64"], "mcm-panel-linux-amd64")):
            installer.record(latest="v0.0.9")
            metadata = json.dumps(release | {"assets": assets}).encode()
            with self.subTest(missing=missing), patch.object(installer, "request", return_value=io.BytesIO(metadata)):
                with self.assertRaisesRegex(installer.Problem, "0.0.1.*" + missing):
                    installer.check()
                state = installer.status()
                self.assertFalse(state["available"])
                self.assertIn(missing, state["check_error"])
                with self.assertRaises(installer.Problem):
                    installer.queue("v0.0.9")

    def test_publish_fills_missing_files_without_replacing_public_assets(self):
        release, files = self.fixture("0.0.1")
        directory = installer.BASE
        for name, data in files.items():
            (directory / name).write_bytes(data)
        metadata = {asset["name"]: asset for asset in release["assets"]}
        release["assets"] = [metadata["setup.py"]]
        uploaded = []

        def gh(*args):
            if args[0] == "api":
                return json.dumps(release)
            self.assertEqual(args[:3], ("release", "upload", "0.0.1"))
            name = Path(args[3]).name
            uploaded.append(name)
            release["assets"].append(metadata[name])

        with patch.object(publisher, "release_url", return_value="test"), patch.object(publisher, "gh", side_effect=gh):
            publisher.publish("0.0.1", directory)
            publisher.publish("0.0.1", directory)
            self.assertEqual(uploaded, ["mcm-panel-linux-amd64", "SHA256SUMS"])
            release["assets"] = [dict(metadata["setup.py"], digest="sha256:" + "0" * 64)]
            with self.assertRaisesRegex(RuntimeError, "Existing asset setup.py differs"):
                publisher.publish("0.0.1", directory)
            self.assertEqual(len(uploaded), 2)

    def test_checksum_failure_cannot_switch_active_version(self):
        release, files = self.fixture("v1.0.0")
        with patch.object(installer, "request", side_effect=lambda url, *_: io.BytesIO(files[url.rsplit("/", 1)[1]])):
            installer.stage(release, "panel")
            installer.select("v1.0.0")
            bad, _ = self.fixture("v1.1.0")
            files["mcm-panel-linux-amd64"] = b"broken"
            with self.assertRaisesRegex(installer.Problem, "Checksum mismatch"):
                installer.stage(bad, "panel")
        self.assertEqual(installer.installed(), "v1.0.0")
        self.assertFalse((installer.BASE / "releases/v1.1.0").exists())

    def test_wrong_newer_or_duplicate_approval(self):
        installer.select("v1.0.0")
        installer.record(latest="v1.2.0")
        for tag in ("v1.0.0", "v0.9.0", "v1.3.0", "main"):
            with self.assertRaises(installer.Problem):
                installer.queue(tag)
        with patch.object(installer, "unit"):
            self.assertEqual(installer.queue("v1.2.0")["phase"], "queued")
            with self.assertRaises(installer.Problem):
                installer.queue("v1.2.0")

    def test_game_patch_waits_for_fleet_backup(self):
        installer.select("v1.0.0")
        installer.save(installer.ETC / "install.json", {"role": "game"})
        installer.record(phase="queued", target="v1.1.0", previous="v1.0.0")
        with installer.fleet_lock({"role": "game"}), patch.object(installer, "stage"), \
                patch.object(installer, "latest", return_value={"tag_name": "v1.1.0"}), \
                patch.object(installer, "activate") as activate:
            with self.assertRaisesRegex(installer.Problem, "Fleet operation"):
                installer.apply_update()
            activate.assert_not_called()
        self.assertEqual(installer.status()["phase"], "failed")
        self.assertEqual(installer.installed(), "v1.0.0")

    def test_interrupted_activation_restores_previous(self):
        installer.select("v1.2.0")
        installer.save(installer.ETC / "install.json", {"role": "game"})
        installer.record(phase="applying", previous="v1.0.0", game_active=False)
        with patch.object(installer, "activate") as activate:
            installer.apply_update()
            activate.assert_called_once_with({"role": "game"}, "v1.0.0", False)
        self.assertEqual(installer.status()["phase"], "failed")


if __name__ == "__main__":
    unittest.main()
