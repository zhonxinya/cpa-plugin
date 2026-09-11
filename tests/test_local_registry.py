import json
import subprocess
import unittest
from pathlib import Path


class LocalRegistryTest(unittest.TestCase):
    def test_local_registry_stays_valid_and_has_no_orphan_artifacts(self):
        root = Path(__file__).parents[1]
        registry_path = root / ".local-store" / "registry.json"
        registry = json.loads(registry_path.read_text(encoding="utf-8"))
        expected = sum(
            len((plugin.get("install") or {}).get("artifacts") or [])
            for plugin in registry.get("plugins") or []
        )

        # The local store is only used for offline artifact verification, so the
        # artifacts directory may legitimately be absent (and is gitignored).
        artifacts_dir = root / ".local-store" / "artifacts"
        if not artifacts_dir.is_dir():
            self.assertEqual(expected, 0, "registry references artifacts that are not present")
            self.skipTest("local archives have not been packaged")

        result = subprocess.run(
            [
                "python3",
                str(root / "scripts" / "check-registry-artifacts.py"),
                str(registry_path),
                "--artifacts-dir",
                str(artifacts_dir),
                "--url-prefix",
                "http://127.0.0.1:18080/artifacts",
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"{expected} artifact(s)", result.stdout)


if __name__ == "__main__":
    unittest.main()
