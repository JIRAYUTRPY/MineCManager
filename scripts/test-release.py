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


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        root = Path(work.name)
        for name in ("STATE", "ETC", "BASE"):
            p = root / name
            p.mkdir()
            self.enterContext(patch.object(installer, name, p))
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
        for change in ({"draft": True}, {"prerelease": True}, {"tag_name": "v1.2.3-beta"},
                       {"tag_name": "main"}, {"tag_name": "../v1.2.3"}):
            with self.subTest(change=change), patch.object(installer, "request", return_value=io.BytesIO(json.dumps(release | change).encode())):
                with self.assertRaises(installer.Problem):
                    installer.latest()
        self.assertGreater(installer.version("v1.10.0"), installer.version("v1.9.9"))

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
