import unittest
from pathlib import Path


PLUGIN_ROOT = Path(__file__).parents[1]
REPO_ROOT = PLUGIN_ROOT.parent
# Each host only reads its own root-level workflow directory, so the plugin
# release pipeline lives at the repository root even though the sources are in
# opencode-go/. GitHub ignores .gitea/ and Gitea ignores .github/.
GITHUB_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "release-opencode-go.yml"
GITEA_WORKFLOW = REPO_ROOT / ".gitea" / "workflows" / "release-opencode-go.yml"
BUILD_SCRIPT = PLUGIN_ROOT / "scripts" / "build-archives.sh"
PUBLISHER = REPO_ROOT / "scripts" / "publish-release.py"


class ReleaseWorkflowTest(unittest.TestCase):
    def read(self, path: Path) -> str:
        if not path.is_file():
            self.fail(f"missing file: {path}")
        return path.read_text(encoding="utf-8")

    def test_workflows_live_where_each_host_reads_them(self):
        self.assertEqual(GITHUB_WORKFLOW.parent.parent.name, ".github")
        self.assertEqual(GITEA_WORKFLOW.parent.parent.name, ".gitea")
        self.read(GITHUB_WORKFLOW)
        self.read(GITEA_WORKFLOW)

    def test_both_workflows_share_one_build_script(self):
        for workflow in (GITHUB_WORKFLOW, GITEA_WORKFLOW):
            self.assertIn(
                "bash opencode-go/scripts/build-archives.sh",
                self.read(workflow),
                f"{workflow} must reuse the shared build script",
            )

    def test_both_workflows_share_one_publisher(self):
        for workflow in (GITHUB_WORKFLOW, GITEA_WORKFLOW):
            text = self.read(workflow)
            self.assertIn("scripts/publish-release.py", text, str(workflow))
            self.assertIn("--plugin-id opencode-go", text)
            self.assertIn("--registry registry.json", text)
        self.read(PUBLISHER)

    def test_release_requires_a_matching_version_tag(self):
        self.assertIn('test "$GITHUB_REF_NAME" = "v$VERSION"', self.read(GITHUB_WORKFLOW))
        self.assertIn('test "$TAG" = "v$VERSION"', self.read(GITEA_WORKFLOW))

    def test_github_workflow_covers_every_platform(self):
        text = self.read(GITHUB_WORKFLOW)
        for goos, goarch in (("darwin", "arm64"), ("linux", "amd64"), ("linux", "arm64")):
            self.assertIn(f"opencode-go_${{VERSION}}_{goos}_{goarch}.zip", text)
        # darwin cgo builds cannot cross-compile from a Linux runner.
        self.assertIn("runner: macos-14", text)

    def test_gitea_workflow_uses_a_pat_and_needs_no_github_api(self):
        text = self.read(GITEA_WORKFLOW)
        self.assertIn("secrets.RELEASE_TOKEN", text)
        self.assertNotIn("softprops/action-gh-release", text)
        self.assertNotIn("api.github.com", text)

    def test_build_script_names_the_library_per_platform(self):
        text = self.read(BUILD_SCRIPT)
        self.assertIn("darwin) ext=dylib", text)
        self.assertIn("windows) ext=dll", text)
        self.assertIn("*) ext=so", text)
        # Archives must be self-verified before the publisher sees them.
        self.assertIn("verify_release_assets.py", text)
        self.assertIn("VERSION file is empty", text)


if __name__ == "__main__":
    unittest.main()
