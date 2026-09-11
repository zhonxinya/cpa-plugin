#!/usr/bin/env python3
"""Publish plugin archives to a Gitea or GitHub release and update the registry.

Both hosts serve release asset downloads from
    {instance}/{owner}/{repo}/releases/download/{tag}/{name}

and expose the same release/asset API shape, so this one script drives either:

    Gitea  ... POST {instance}/api/v1/repos/{owner}/{repo}/releases
    GitHub ... POST https://api.github.com/repos/{owner}/{repo}/releases

It creates the release when missing, uploads every archive named
<plugin-id>_<version>_<goos>_<goarch>.zip, re-downloads each asset to prove the
published bytes match the local SHA-256, and can merge the resulting artifacts
into registry.json in one step.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


ARCHIVE_NAME = re.compile(
    r"^(?P<id>[A-Za-z0-9][A-Za-z0-9._-]*)_"
    r"(?P<version>[0-9][0-9A-Za-z.+-]*)_"
    r"(?P<goos>[a-z0-9]+)_(?P<goarch>[a-z0-9]+)\.zip$"
)
# Checked in order when --token-env is not given.
TOKEN_ENV_CANDIDATES = ("GITEA_TOKEN", "GITHUB_TOKEN", "GH_TOKEN")
DEFAULT_TIMEOUT = 60.0


def fail(message: str) -> None:
    raise SystemExit(message)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--api-url", required=True, help="host base URL, e.g. https://gitea.example.com or https://github.com")
    parser.add_argument("--repo", required=True, help="owner/repo on that host")
    parser.add_argument("--tag", required=True, help="release tag, e.g. v0.1.0")
    parser.add_argument("--plugin-id", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--asset", type=Path, action="append", required=True, help="archive to publish (repeatable)")
    parser.add_argument("--repository-url", help="repository URL written to registry.json (defaults to <api-url>/<repo>)")
    parser.add_argument(
        "--api-base",
        help="REST API base override; defaults to https://api.github.com for github.com and <api-url>/api/v1 otherwise",
    )
    parser.add_argument(
        "--uploads-base",
        help="asset upload base override; defaults to https://uploads.github.com for GitHub and <api-base> otherwise",
    )
    parser.add_argument(
        "--host-flavor",
        choices=("auto", "github", "gitea"),
        default="auto",
        help="asset path style; auto detects GitHub from the API base (default: auto)",
    )
    parser.add_argument("--token-env", help="environment variable holding the API token (default: " + ", ".join(TOKEN_ENV_CANDIDATES) + ")")
    parser.add_argument("--token-file", type=Path, help="file holding the API token")
    parser.add_argument("--replace", action="store_true", help="delete and re-upload an asset that already exists")
    parser.add_argument("--dry-run", action="store_true", help="print the plan without contacting the host")
    parser.add_argument("--skip-download-verify", action="store_true", help="do not re-download published assets")
    parser.add_argument("--registry", type=Path, help="registry.json to update in place")
    parser.add_argument("--registry-name")
    parser.add_argument("--registry-description")
    parser.add_argument("--registry-author", default="zhonxinya")
    parser.add_argument("--registry-license", default="MIT")
    parser.add_argument("--registry-homepage")
    parser.add_argument("--registry-tag", action="append", dest="registry_tags", default=None)
    parser.add_argument("--timeout", type=float, default=DEFAULT_TIMEOUT)
    return parser.parse_args()


def api_base_for(instance: str, override: str | None) -> str:
    """Resolve the REST API base for a Gitea instance or github.com."""
    if override:
        return override.rstrip("/")
    host = (urllib.parse.urlparse(instance).hostname or "").lower()
    if host == "github.com":
        return "https://api.github.com"
    return f"{instance}/api/v1"


def uploads_base_for(api_base: str, flavor: str, override: str | None) -> str:
    """Resolve the asset upload base.

    GitHub refuses asset uploads on api.github.com and serves them from
    uploads.github.com (GitHub Enterprise uses /api/uploads). Gitea has no such
    split, so its uploads share the API base.
    """
    if override:
        return override.rstrip("/")
    base = api_base.rstrip("/")
    if flavor != "github":
        return base
    if base == "https://api.github.com":
        return "https://uploads.github.com"
    if base.endswith("/api/v3"):
        return base[: -len("/api/v3")] + "/api/uploads"
    return base


def host_flavor_for(api_base: str, override: str | None) -> str:
    """Classify the release host, because asset paths differ between them."""
    if override and override != "auto":
        return override
    host = (urllib.parse.urlparse(api_base).hostname or "").lower()
    if host == "api.github.com" or host == "uploads.github.com":
        return "github"
    if api_base.rstrip("/").endswith("/api/v3"):
        return "github"
    return "gitea"


def upload_endpoint(
    release: dict,
    api: str,
    repo: str,
    flavor: str,
    uploads_base: str,
) -> str:
    """Resolve the asset upload URL for one release.

    GitHub returns the exact upload URL as a hypermedia template in upload_url
    and its host differs from the API host, so prefer it. Fall back to the
    derived uploads base when a host does not provide it.
    """
    template = str(release.get("upload_url") or "").strip()
    if template:
        return template.split("{", 1)[0]
    base = uploads_base.rstrip("/") or api.rstrip("/")
    return f"{base}/repos/{repo}/releases/{release['id']}/assets"


def asset_delete_url(api: str, repo: str, flavor: str, release_id: int, asset_id: int) -> str:
    """Build the asset delete URL.

    GitHub addresses an asset directly (/releases/assets/{id}); Gitea nests it
    under the release (/releases/{id}/assets/{id}).
    """
    base = api.rstrip("/")
    if flavor == "github":
        return f"{base}/repos/{repo}/releases/assets/{asset_id}"
    return f"{base}/repos/{repo}/releases/{release_id}/assets/{asset_id}"


def resolve_token(args: argparse.Namespace) -> str:
    names = [args.token_env] if args.token_env else list(TOKEN_ENV_CANDIDATES)
    for name in names:
        token = os.environ.get(name, "").strip()
        if token:
            return token
    if args.token_file:
        try:
            token = args.token_file.read_text(encoding="utf-8").strip()
        except OSError as error:
            fail(str(error))
        if token:
            return token
    fail(
        "no API token: set "
        + " or ".join(names)
        + ", or pass --token-file (the token needs the repository write scope)"
    )


def api_request(
    method: str,
    url: str,
    token: str,
    *,
    data: bytes | None = None,
    content_type: str | None = None,
    timeout: float,
) -> tuple[int, bytes]:
    request = urllib.request.Request(url, data=data, method=method)
    request.add_header("Authorization", f"token {token}")
    request.add_header("Accept", "application/json")
    if content_type:
        request.add_header("Content-Type", content_type)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()
    except urllib.error.URLError as error:
        fail(f"{method} {url}: {error.reason}")


def sha256_file(path: Path) -> tuple[str, int]:
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
            size += len(chunk)
    return digest.hexdigest(), size


def parse_archive(path: Path, plugin_id: str, version: str) -> tuple[str, str]:
    match = ARCHIVE_NAME.fullmatch(path.name)
    if not match:
        fail(
            f"{path.name}: expected <plugin-id>_<version>_<goos>_<goarch>.zip "
            "so the plugin store can resolve the platform"
        )
    if match.group("id") != plugin_id:
        fail(f"{path.name}: plugin id {match.group('id')!r} does not match {plugin_id!r}")
    if match.group("version") != version:
        fail(f"{path.name}: version {match.group('version')!r} does not match {version!r}")
    return match.group("goos"), match.group("goarch")


def download_url(repository_url: str, tag: str, name: str) -> str:
    return f"{repository_url}/releases/download/{tag}/{urllib.parse.quote(name)}"


def find_release(api: str, repo: str, tag: str, token: str, timeout: float) -> dict | None:
    status, body = api_request(
        "GET", f"{api}/repos/{repo}/releases/tags/{urllib.parse.quote(tag)}", token, timeout=timeout
    )
    if status == 200:
        return json.loads(body)
    if status == 404:
        return None
    fail(f"read release {tag}: HTTP {status}: {body[:200]!r}")


def ensure_release(api: str, repo: str, tag: str, token: str, timeout: float) -> dict:
    release = find_release(api, repo, tag, token, timeout)
    if release is not None:
        return release
    payload = json.dumps(
        {"tag_name": tag, "name": tag, "draft": False, "prerelease": False}
    ).encode("utf-8")
    status, body = api_request(
        "POST",
        f"{api}/repos/{repo}/releases",
        token,
        data=payload,
        content_type="application/json",
        timeout=timeout,
    )
    if status == 201:
        return json.loads(body)
    if status in (409, 422):
        # Gitea answers 409 and GitHub 422 when the tag already has a release,
        # which can happen when a concurrent run wins the create race.
        release = find_release(api, repo, tag, token, timeout)
        if release is not None:
            return release
    fail(f"create release {tag}: HTTP {status}: {body[:200]!r}")


def upload_asset(
    api: str,
    repo: str,
    release: dict,
    asset: Path,
    token: str,
    timeout: float,
    replace: bool,
    flavor: str,
    uploads_base: str,
) -> dict:
    name = asset.name
    existing = next((item for item in release.get("assets") or [] if item.get("name") == name), None)
    if existing is not None:
        if not replace:
            fail(f"{name} is already attached to release {release['tag_name']}; pass --replace to overwrite it")
        status, body = api_request(
            "DELETE",
            asset_delete_url(api, repo, flavor, release["id"], existing["id"]),
            token,
            timeout=timeout,
        )
        if status not in (204, 200):
            fail(f"delete existing asset {name}: HTTP {status}: {body[:200]!r}")
    query = urllib.parse.urlencode({"name": name})
    status, body = api_request(
        "POST",
        f"{upload_endpoint(release, api, repo, flavor, uploads_base)}?{query}",
        token,
        data=asset.read_bytes(),
        content_type="application/zip",
        timeout=timeout,
    )
    if status != 201:
        fail(f"upload {name}: HTTP {status}: {body[:200]!r}")
    uploaded = json.loads(body)
    # GitHub silently renames assets containing unusual characters, which would
    # break the registry URL, so refuse to continue on a name mismatch.
    returned = str(uploaded.get("name") or "").strip()
    if returned and returned != name:
        fail(f"host stored the asset as {returned!r} instead of {name!r}; registry URLs would be wrong")
    return uploaded


def verify_download(url: str, expected_sha: str, token: str, timeout: float) -> None:
    status, body = api_request("GET", url, token, timeout=timeout)
    if status != 200:
        fail(f"published asset is not downloadable: {url} returned HTTP {status}")
    actual = hashlib.sha256(body).hexdigest()
    if actual != expected_sha:
        fail(f"{url}: published sha256 {actual} does not match local {expected_sha}")


def registry_artifacts(entries: list[dict]) -> list[dict]:
    return sorted(entries, key=lambda item: (item["goos"], item["goarch"]))


def update_registry(args: argparse.Namespace, artifacts: list[dict], repository_url: str) -> None:
    path = args.registry
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(str(error))
    if data.get("schema_version") != 2:
        fail(f"{path}: schema_version must be 2")
    plugins = data.get("plugins")
    if not isinstance(plugins, list):
        fail(f"{path}: plugins must be an array")

    entry = next((item for item in plugins if str(item.get("id", "")).strip() == args.plugin_id), None)
    if entry is None:
        entry = {"id": args.plugin_id, "author": args.registry_author, "license": args.registry_license}
        plugins.append(entry)
        plugins.sort(key=lambda item: str(item.get("id", "")))

    # Preserve hand-written metadata; only overwrite what the caller supplied.
    entry["id"] = args.plugin_id
    entry["version"] = args.version
    entry["repository"] = repository_url
    entry.setdefault("name", args.registry_name or args.plugin_id)
    entry.setdefault("description", args.registry_description or f"{args.plugin_id} plugin")
    entry.setdefault("homepage", args.registry_homepage or repository_url)
    entry.setdefault("license", args.registry_license)
    entry.setdefault("author", args.registry_author)
    entry.setdefault("tags", args.registry_tags or ["Usage"])
    for key, value in (
        ("name", args.registry_name),
        ("description", args.registry_description),
        ("homepage", args.registry_homepage),
        ("tags", args.registry_tags),
    ):
        if value:
            entry[key] = value
    entry["install"] = {"type": "direct", "artifacts": registry_artifacts(artifacts)}

    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"updated {path}: {args.plugin_id} {args.version}, {len(artifacts)} artifact(s)")


def main() -> int:
    args = parse_args()
    instance = args.api_url.rstrip("/")
    if instance.endswith("/api/v1"):
        instance = instance[: -len("/api/v1")]
    api = api_base_for(instance, args.api_base)
    flavor = host_flavor_for(api, args.host_flavor)
    uploads_base = uploads_base_for(api, flavor, args.uploads_base)
    if "/" not in args.repo.strip("/"):
        fail("--repo must be owner/repo")
    repo = args.repo.strip("/")
    repository_url = (args.repository_url or f"{instance}/{repo}").rstrip("/")
    tag = args.tag.strip()
    if tag != f"v{args.version}":
        fail(f"--tag {tag!r} must equal v{args.version} so registry URLs stay canonical")

    artifacts: list[dict] = []
    for asset in args.asset:
        if not asset.is_file():
            fail(f"{asset}: archive does not exist")
        goos, goarch = parse_archive(asset, args.plugin_id, args.version)
        digest, size = sha256_file(asset)
        url = download_url(repository_url, tag, asset.name)
        artifacts.append({"goos": goos, "goarch": goarch, "url": url, "sha256": digest})
        print(f"{asset.name}: {goos}/{goarch}, {size} bytes, sha256 {digest}")

    if args.dry_run:
        print(f"dry run: host flavor {flavor}, api {api}")
        print(f"dry run: would publish {len(artifacts)} asset(s) to {api}/repos/{repo} release {tag}")
        if flavor == "github":
            print(f"dry run: asset uploads would use {uploads_base}")
        if args.registry:
            print(f"dry run: would update {args.registry}")
        return 0

    token = resolve_token(args)
    release = ensure_release(api, repo, tag, token, args.timeout)
    print(f"release {tag} id={release['id']}")
    for asset, artifact in zip(args.asset, artifacts):
        uploaded = upload_asset(
            api, repo, release, asset, token, args.timeout, args.replace, flavor, uploads_base
        )
        print(f"uploaded {uploaded.get('name') or asset.name}")
        if not args.skip_download_verify:
            verify_download(artifact["url"], artifact["sha256"], token, args.timeout)
            print(f"verified {artifact['url']}")

    if args.registry:
        update_registry(args, artifacts, repository_url)

    print("\nregistry artifacts:")
    print(json.dumps(registry_artifacts(artifacts), indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
