import hashlib
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
import zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse


ROOT = Path(__file__).parents[1]
PUBLISHER = ROOT / "scripts" / "publish-release.py"
TOKEN = "test-token"


def write_archive(path: Path, payload: bytes) -> str:
    """Write a CPA-style archive and return its sha256."""
    with zipfile.ZipFile(path, "w") as writer:
        writer.writestr("plugin.so", payload)
    return hashlib.sha256(path.read_bytes()).hexdigest()


class StubGitea:
    """Minimal Gitea release API double covering create/upload/download/delete."""

    def __init__(self):
        self.releases = {}
        self.blobs = {}
        self.downloads = []
        self.next_release_id = 1
        self.next_asset_id = 1
        stub = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *args):  # keep test output clean
                pass

            def _send(self, status, payload=b"", content_type="application/json"):
                self.send_response(status)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                if payload:
                    self.wfile.write(payload)

            def _json(self, status, value):
                self._send(status, json.dumps(value).encode("utf-8"))

            def _read_body(self):
                length = int(self.headers.get("Content-Length") or 0)
                return self.rfile.read(length) if length else b""

            def _authorized(self):
                if self.headers.get("Authorization") != f"token {TOKEN}":
                    self._json(401, {"message": "unauthorized"})
                    return False
                return True

            def do_GET(self):
                parsed = urlparse(self.path)
                parts = [unquote(part) for part in parsed.path.strip("/").split("/")]
                if parsed.path.startswith("/api/v1/"):
                    if not self._authorized():
                        return
                    # /api/v1/repos/{owner}/{repo}/releases/tags/{tag}
                    if len(parts) == 8 and parts[5:7] == ["releases", "tags"]:
                        release = stub.releases.get(parts[7])
                        return self._json(200, release) if release else self._json(404, {"message": "not found"})
                    return self._json(404, {"message": "not found"})
                # /{owner}/{repo}/releases/download/{tag}/{name}
                if len(parts) == 6 and parts[2:4] == ["releases", "download"]:
                    blob = stub.blobs.get((parts[4], parts[5]))
                    if blob is None:
                        return self._json(404, {"message": "not found"})
                    stub.downloads.append(parsed.path)
                    return self._send(200, blob, content_type="application/octet-stream")
                return self._json(404, {"message": "not found"})

            def do_POST(self):
                parsed = urlparse(self.path)
                parts = [unquote(part) for part in parsed.path.strip("/").split("/")]
                if not self._authorized():
                    return
                body = self._read_body()
                # /api/v1/repos/{owner}/{repo}/releases
                if len(parts) == 6 and parts[5] == "releases":
                    payload = json.loads(body)
                    tag = payload["tag_name"]
                    if tag in stub.releases:
                        return self._json(409, {"message": "release exists"})
                    release = {
                        "id": stub.next_release_id,
                        "tag_name": tag,
                        "name": payload.get("name") or tag,
                        "assets": [],
                    }
                    stub.next_release_id += 1
                    stub.releases[tag] = release
                    return self._json(201, release)
                # /api/v1/repos/{owner}/{repo}/releases/{id}/assets?name=...
                if len(parts) == 8 and parts[5] == "releases" and parts[7] == "assets":
                    release = next((item for item in stub.releases.values() if str(item["id"]) == parts[6]), None)
                    if release is None:
                        return self._json(404, {"message": "release not found"})
                    name = parse_qs(parsed.query).get("name", [""])[0]
                    if not name:
                        return self._json(400, {"message": "name required"})
                    asset = {
                        "id": stub.next_asset_id,
                        "name": name,
                        "browser_download_url": f"{stub.base}/{REPO}/releases/download/{release['tag_name']}/{name}",
                    }
                    stub.next_asset_id += 1
                    release["assets"].append(asset)
                    stub.blobs[(release["tag_name"], name)] = body
                    return self._json(201, asset)
                return self._json(404, {"message": "not found"})

            def do_DELETE(self):
                parsed = urlparse(self.path)
                parts = [unquote(part) for part in parsed.path.strip("/").split("/")]
                if not self._authorized():
                    return
                # /api/v1/repos/{owner}/{repo}/releases/{id}/assets/{asset_id}
                if len(parts) == 9 and parts[5] == "releases" and parts[7] == "assets":
                    release = next((item for item in stub.releases.values() if str(item["id"]) == parts[6]), None)
                    if release is None:
                        return self._json(404, {"message": "release not found"})
                    asset = next((item for item in release["assets"] if str(item["id"]) == parts[8]), None)
                    if asset is None:
                        return self._json(404, {"message": "asset not found"})
                    release["assets"].remove(asset)
                    stub.blobs.pop((release["tag_name"], asset["name"]), None)
                    return self._send(204)
                return self._json(404, {"message": "not found"})

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.base = f"http://127.0.0.1:{self.httpd.server_port}"
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()
        self.thread.join(timeout=5)


REPO = "zhonxinya/cpa-plugin"


class PublishGiteaReleaseTest(unittest.TestCase):
    def setUp(self):
        self.server = StubGitea()
        self.addCleanup(self.server.close)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.dist = self.root / "dist"
        self.dist.mkdir()
        self.darwin = self.dist / "opencode-go_0.1.0_darwin_arm64.zip"
        self.linux = self.dist / "opencode-go_0.1.0_linux_amd64.zip"
        self.darwin_sha = write_archive(self.darwin, b"darwin")
        self.linux_sha = write_archive(self.linux, b"linux")
        self.registry = self.root / "registry.json"
        self.registry.write_text(
            json.dumps(
                {
                    "schema_version": 2,
                    "plugins": [
                        {
                            "id": "opencode-go",
                            "name": "OpenCode Go",
                            "description": "OpenCode Go usage and provider.",
                            "author": "zhonxinya",
                            "version": "0.0.0",
                            "repository": "https://github.com/zhonxinya/cpa-plugin",
                            "license": "MIT",
                            "tags": ["Usage"],
                            "install": {"type": "direct", "artifacts": []},
                        }
                    ],
                },
                indent=2,
            )
            + "\n",
            encoding="utf-8",
        )

    def run_publisher(self, *extra, token=TOKEN, env=None):
        environment = dict(os.environ)
        # Clear every candidate variable so the token assertions are deterministic.
        for name in ("GITEA_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"):
            environment.pop(name, None)
        if token is not None:
            environment["GITEA_TOKEN"] = token
        if env:
            environment.update(env)
        return subprocess.run(
            [
                sys.executable,
                str(PUBLISHER),
                "--api-url",
                self.server.base,
                "--repo",
                REPO,
                "--tag",
                "v0.1.0",
                "--plugin-id",
                "opencode-go",
                "--version",
                "0.1.0",
                "--asset",
                str(self.darwin),
                "--asset",
                str(self.linux),
                *extra,
            ],
            capture_output=True,
            text=True,
            env=environment,
        )

    def test_publishes_assets_and_updates_registry(self):
        result = self.run_publisher("--registry", str(self.registry))

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("v0.1.0", self.server.releases)
        release = self.server.releases["v0.1.0"]
        self.assertEqual(
            sorted(asset["name"] for asset in release["assets"]),
            [self.darwin.name, self.linux.name],
        )
        # The publisher must prove the published bytes are retrievable and intact.
        self.assertEqual(len(self.server.downloads), 2)

        registry = json.loads(self.registry.read_text(encoding="utf-8"))
        plugin = registry["plugins"][0]
        self.assertEqual(plugin["version"], "0.1.0")
        self.assertEqual(plugin["repository"], f"{self.server.base}/{REPO}")
        artifacts = plugin["install"]["artifacts"]
        self.assertEqual(
            [(item["goos"], item["goarch"]) for item in artifacts],
            [("darwin", "arm64"), ("linux", "amd64")],
        )
        expected = {
            "darwin": (f"{self.server.base}/{REPO}/releases/download/v0.1.0/{self.darwin.name}", self.darwin_sha),
            "linux": (f"{self.server.base}/{REPO}/releases/download/v0.1.0/{self.linux.name}", self.linux_sha),
        }
        for artifact in artifacts:
            url, sha = expected[artifact["goos"]]
            self.assertEqual(artifact["url"], url)
            self.assertEqual(artifact["sha256"], sha)
        # Hand-written metadata survives the update.
        self.assertEqual(plugin["name"], "OpenCode Go")
        self.assertEqual(plugin["tags"], ["Usage"])

    def test_generated_registry_passes_store_validators(self):
        self.run_publisher("--registry", str(self.registry))

        validate = subprocess.run(
            [sys.executable, str(ROOT / "scripts" / "validate-registry.py"), str(self.registry)],
            capture_output=True,
            text=True,
        )
        self.assertEqual(validate.returncode, 0, validate.stderr)

        check = subprocess.run(
            [
                sys.executable,
                str(ROOT / "scripts" / "check-registry-artifacts.py"),
                str(self.registry),
                "--artifacts-dir",
                str(self.dist),
                "--url-prefix",
                f"{self.server.base}/artifacts",
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(check.returncode, 0, check.stderr)
        self.assertIn("2 artifact(s)", check.stdout)

    def test_rerun_requires_replace_then_succeeds(self):
        first = self.run_publisher()
        self.assertEqual(first.returncode, 0, first.stderr)

        second = self.run_publisher()
        self.assertNotEqual(second.returncode, 0)
        self.assertIn("--replace", second.stderr)

        third = self.run_publisher("--replace")
        self.assertEqual(third.returncode, 0, third.stderr)
        release = self.server.releases["v0.1.0"]
        self.assertEqual(len(release["assets"]), 2)

    def test_rejects_tag_that_is_not_v_version(self):
        result = self.run_publisher("--tag", "0.1.0")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must equal", result.stderr)

    def test_rejects_misnamed_archive(self):
        stray = self.dist / "opencode-go.zip"
        stray.write_bytes(b"stray")

        result = self.run_publisher("--asset", str(stray))

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("expected <plugin-id>_<version>", result.stderr)

    def test_requires_token(self):
        result = self.run_publisher(token=None)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no API token", result.stderr)

    def test_dry_run_touches_nothing(self):
        result = self.run_publisher("--dry-run", "--registry", str(self.registry))

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.server.releases, {})
        self.assertIn("dry run", result.stdout)

    def test_accepts_github_token_when_gitea_token_is_absent(self):
        result = self.run_publisher(token=None, env={"GITHUB_TOKEN": TOKEN})

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("v0.1.0", self.server.releases)

    def test_github_mode_targets_the_github_api_base(self):
        # Simulate the GitHub invocation: instance is github.com, so the API base
        # defaults to api.github.com, which --api-base redirects at the stub.
        result = self.run_publisher(
            "--api-url",
            "https://github.com",
            "--api-base",
            f"{self.server.base}/api/v1",
            "--repository-url",
            f"{self.server.base}/{REPO}",
            "--registry",
            str(self.registry),
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        registry = json.loads(self.registry.read_text(encoding="utf-8"))
        artifacts = registry["plugins"][0]["install"]["artifacts"]
        for artifact in artifacts:
            self.assertTrue(
                artifact["url"].startswith(f"{self.server.base}/{REPO}/releases/download/v0.1.0/"),
                artifact["url"],
            )
            self.assertEqual(len(artifact["sha256"]), 64)

    def test_dry_run_reports_the_github_api_endpoint(self):
        result = self.run_publisher(
            "--api-url", "https://github.com", "--dry-run"
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("https://api.github.com/repos/", result.stdout)

    def test_api_base_defaults_per_host(self):
        source = PUBLISHER.read_text(encoding="utf-8")

        self.assertIn('if host == "github.com":', source)
        self.assertIn('return "https://api.github.com"', source)
        self.assertIn('return f"{instance}/api/v1"', source)


if __name__ == "__main__":
    unittest.main()
