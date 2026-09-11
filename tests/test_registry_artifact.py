import json
import re
import subprocess
import sys
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]
ARCHIVE_NAME = re.compile(
    r"^(?P<id>[A-Za-z0-9][A-Za-z0-9._-]*)_"
    r"(?P<version>[0-9][0-9A-Za-z.+-]*)_"
    r"(?P<goos>[a-z0-9]+)_(?P<goarch>[a-z0-9]+)\.zip$"
)
SHA256 = re.compile(r"^[0-9a-f]{64}$")


class RegistryArtifactTest(unittest.TestCase):
    """Conformance checks for the shipped registry.json.

    The registry starts empty: the first tagged release runs
    scripts/publish-release.py, which appends the plugin entry with the real
    release URL and checksum. These tests therefore validate every entry that
    exists rather than hard-coding a specific plugin.
    """

    def setUp(self):
        self.registry = json.loads((ROOT / "registry.json").read_text(encoding="utf-8"))
        self.plugins = self.registry.get("plugins") or []

    def test_registry_is_schema_two_with_a_plugin_list(self):
        self.assertEqual(self.registry["schema_version"], 2)
        self.assertIsInstance(self.registry["plugins"], list)

    def test_every_entry_uses_the_direct_install_contract(self):
        for plugin in self.plugins:
            with self.subTest(plugin=plugin.get("id")):
                install = plugin["install"]
                self.assertEqual(install["type"], "direct")
                self.assertTrue(install["artifacts"], "direct install needs at least one artifact")

    def test_every_artifact_matches_the_store_filename_convention(self):
        for plugin in self.plugins:
            for artifact in plugin["install"]["artifacts"]:
                with self.subTest(plugin=plugin["id"], platform=(artifact["goos"], artifact["goarch"])):
                    url = artifact["url"]
                    name = url.rsplit("/", 1)[-1]
                    match = ARCHIVE_NAME.fullmatch(name)
                    self.assertIsNotNone(match, f"{name} does not match <id>_<version>_<goos>_<goarch>.zip")
                    self.assertEqual(match.group("id"), plugin["id"])
                    self.assertEqual(match.group("version"), plugin["version"])
                    self.assertEqual(match.group("goos"), artifact["goos"])
                    self.assertEqual(match.group("goarch"), artifact["goarch"])

    def test_every_artifact_has_a_release_url_and_checksum(self):
        for plugin in self.plugins:
            repository = plugin["repository"].rstrip("/")
            for artifact in plugin["install"]["artifacts"]:
                with self.subTest(plugin=plugin["id"]):
                    self.assertEqual(
                        artifact["url"],
                        f"{repository}/releases/download/v{plugin['version']}/"
                        f"{plugin['id']}_{plugin['version']}_{artifact['goos']}_{artifact['goarch']}.zip",
                    )
                    self.assertTrue(
                        SHA256.fullmatch(artifact["sha256"]),
                        f"invalid sha256 for {plugin['id']}",
                    )

    def test_registry_checker_accepts_the_shipped_registry(self):
        result = subprocess.run(
            [
                sys.executable,
                str(ROOT / "scripts" / "check-registry-artifacts.py"),
                str(ROOT / "registry.json"),
                "--artifacts-dir",
                str(ROOT / ".local-store" / "artifacts"),
                "--url-prefix",
                "http://127.0.0.1:18080/artifacts",
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
