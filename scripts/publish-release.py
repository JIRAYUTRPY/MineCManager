#!/usr/bin/env python3
"""Attach tested assets to the GitHub Release for the tag being built."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys


def gh(*args):
    return subprocess.check_output(["gh", *args], text=True).strip()


def release_url(tag):
    result = subprocess.run(["gh", "release", "view", tag, "--json", "apiUrl", "--jq", ".apiUrl"],
                            text=True, capture_output=True)
    if result.returncode == 0:
        return result.stdout.strip()
    if not any(message in result.stderr.lower() for message in ("release not found", "http 404")):
        raise RuntimeError(result.stderr.strip())
    gh("release", "create", tag, "--draft", "--verify-tag", "--title", tag, "--generate-notes")
    return gh("release", "view", tag, "--json", "apiUrl", "--jq", ".apiUrl")


def missing_assets(release, digests):
    uploaded = {a["name"]: a for a in release["assets"]}
    missing = []
    for name, digest in digests.items():
        asset = uploaded.get(name)
        if asset is None:
            missing.append(name)
        elif asset.get("state") != "uploaded" or asset.get("digest") != "sha256:" + digest:
            raise RuntimeError(f"Existing asset {name} differs from this build. Publish a new version.")
    return missing


def publish(tag, directory):
    manifest = directory / "SHA256SUMS"
    subprocess.run(["sha256sum", "--check", "--strict", manifest.name], cwd=directory, check=True)
    files = sorted(path for path in directory.iterdir() if path.is_file() and path != manifest)
    # Clients check the manifest before offering the update. Upload it last.
    digests = {}
    for path in [*files, manifest]:
        with path.open("rb") as stream:
            digests[path.name] = hashlib.file_digest(stream, "sha256").hexdigest()
    url = release_url(tag)
    release = json.loads(gh("api", url))
    if release["tag_name"] != tag or release["prerelease"]:
        raise RuntimeError("Expected the stable release for " + tag)
    pending = missing_assets(release, digests)
    if pending and release.get("immutable"):
        raise RuntimeError("Published release is immutable. Push a new version tag so CI can attach assets to a draft.")
    for name in pending:
        gh("release", "upload", tag, str(directory / name))
    if pending and missing_assets(json.loads(gh("api", url)), digests):
        raise RuntimeError("GitHub has not confirmed all release assets. Retry the publish job.")
    if release["draft"]:
        gh("release", "edit", tag, "--draft=false")


if __name__ == "__main__":
    try:
        publish(sys.argv[1], Path(sys.argv[2]))
    except RuntimeError as error:
        sys.exit(str(error))
