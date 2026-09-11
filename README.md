# CPA 插件仓库

[CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) 插件集合。

## 插件

| ID | 说明 | 源码 |
|---|---|---|
| `opencode-go` | OpenCode Go 订阅用量看板，并把 OpenCode Go 作为 CPA 供应商提供（chat-completions / responses / anthropic）。 | 本仓库 [opencode-go/](opencode-go/) |

## 多架构 Release

插件工件遵循 CLIProxyAPI 插件商店的标准命名：

~~~text
<id>_<version>_<goos>_<goarch>.zip
~~~

ZIP 根目录只包含平台动态库：

~~~text
opencode-go_0.1.0_linux_amd64.zip     # opencode-go.so
opencode-go_0.1.0_darwin_arm64.zip    # opencode-go.dylib
~~~

`opencode-go` 的源码、构建与 Release 工作流都在本仓库，registry 条目由发布脚本在首次发布时自动写入。

## 安装

### 通过插件商店安装

在 CLIProxyAPI 配置中添加自定义商店源：

~~~yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/zhonxinya/cpa-plugin/main/registry.json"
~~~

刷新插件商店后，安装或更新 `opencode-go`。

### 直接下载 Release

以 Linux amd64 为例（把版本号换成实际发布的版本）：

~~~bash
curl -L -o opencode-go_0.1.0_linux_amd64.zip \
  https://github.com/zhonxinya/cpa-plugin/releases/download/v0.1.0/opencode-go_0.1.0_linux_amd64.zip
unzip opencode-go_0.1.0_linux_amd64.zip
~~~

将解压出的 `opencode-go.so` 放入 CPA 的插件目录，并在配置中启用：

~~~yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    opencode-go:
      enabled: true
~~~

插件商店安装会自动按 `GOOS/GOARCH` 选择工件并校验 SHA-256。

## 远程更新

在 CPA 插件商店中添加：

~~~text
https://raw.githubusercontent.com/zhonxinya/cpa-plugin/main/registry.json
~~~

然后在商店 UI 中安装或更新 `opencode-go`。

## Registry 与验证

正式 registry 位于 [registry.json](registry.json)。它初始为空，第一次发布 `opencode-go` 时由 `scripts/publish-release.py` 写入真实的 Release URL 与 SHA-256：

~~~bash
# 发布后 registry.json 会自动包含 opencode-go 的工件条目
GITHUB_TOKEN=... python3 scripts/publish-release.py \
  --api-url https://github.com \
  --repo zhonxinya/cpa-plugin \
  --tag v0.1.0 --plugin-id opencode-go --version 0.1.0 \
  --asset opencode-go/dist/opencode-go_0.1.0_linux_amd64.zip \
  --registry registry.json
~~~

本地验证：

~~~bash
python3 scripts/validate-registry.py registry.json
python3 scripts/check-registry-artifacts.py registry.json \
  --artifacts-dir .local-store/artifacts \
  --url-prefix http://127.0.0.1:18080/artifacts
python3 -m unittest discover -s tests -p 'test_*.py' -v
~~~

`check-registry-artifacts.py` 会检查 Release URL 的平台、版本、文件名及 SHA-256 格式。

## 发布（GitHub / Gitea）

两个平台的 Release 附件下载地址**完全一致**：

~~~text
{实例}/{owner}/{repo}/releases/download/{tag}/{文件名}
~~~

REST API 的 Create release / Upload asset 接口形状也一致，所以：

- `registry.json`、`check-registry-artifacts.py` 和 CPA 插件商店无需任何平台特殊处理；
- 一个发布脚本 `scripts/publish-release.py` 同时驱动 GitHub 与 Gitea（`--api-url` 决定目标）；
- 一个构建脚本 `opencode-go/scripts/build-archives.sh` 被两套 CI 共用（脚本会自动 `cd` 到插件目录，可从任意位置调用）。

唯一要求是文件名继续遵循商店约定 `<id>_<version>_<goos>_<goarch>.zip`。

### CI 目录与平台覆盖

GitHub 只读 `.github/workflows/`，Gitea 只读 `.gitea/workflows/`，两者互不识别，因此仓库根目录同时保留两套（各自独立可用）：

| 宿主 | 工作流 | 覆盖平台 |
| --- | --- | --- |
| GitHub | `.github/workflows/release-opencode-go.yml` | `darwin/arm64`（macos-14）、`linux/amd64`、`linux/arm64` |
| Gitea | `.gitea/workflows/release-opencode-go.yml` | `linux/amd64`、`linux/arm64` |

两套都会在 `main` 分支推送或 PR 时由 `validate-store.yml` 先跑校验。

> **macOS 工件只能由 macOS 主机产出**（cgo 无法跨平台编译 darwin）。Gitea Runner 通常是 Linux 容器，因此 Gitea 侧不产出 darwin 工件；需要时用下面的「本地手工发布」在 macOS 上补传。

### 一次性准备

**GitHub**：无需额外配置。工作流使用自动注入的 `GITHUB_TOKEN`（已声明 `permissions: contents: write`）。

**Gitea**：

1. 生成 Personal Access Token（**Settings → Applications → Generate New Token**），作用域至少包含 `repository` 读写。
2. 存为仓库 Secret（**仓库 → Settings → Actions → Secrets**），名称 `RELEASE_TOKEN`。
3. 仓库开启 Actions（**Settings → Enable Repository Actions**）并注册 Gitea Runner。

### 打标签即发布

~~~bash
# VERSION 文件必须与标签一致，否则工作流直接失败
git tag v0.1.0
git push origin v0.1.0
~~~

流水线动作：

1. 运行 Go 与 Python 测试；
2. 用 `-buildmode=c-shared` 构建并按平台命名动态库，打包后调用 `verify_release_assets.py` 自校验；
3. 调用 `scripts/publish-release.py` 创建 Release、上传附件、**重新下载并比对 SHA-256**；
4. 用真实 SHA-256 更新 `registry.json` 并提交回默认分支（可用仓库变量 `REGISTRY_BRANCH` 覆盖分支名；若分支受保护，请删除该步骤改为人工提交）。

### 本地手工发布

任何平台、任何宿主都可以用同一个脚本发布已构建好的工件（无需 CI）：

~~~bash
# GitHub
GITHUB_TOKEN=... python3 scripts/publish-release.py \
  --api-url https://github.com \
  --repo zhonxinya/cpa-plugin \
  --tag v0.1.0 \
  --plugin-id opencode-go \
  --version 0.1.0 \
  --asset opencode-go/dist/opencode-go_0.1.0_darwin_arm64.zip \
  --registry registry.json \
  --registry-name "OpenCode Go" \
  --registry-description "OpenCode Go 订阅用量与 provider 插件。" \
  --registry-tag Usage --registry-tag Provider
~~~

把 `--api-url` 换成你的 Gitea 实例地址即发布到 Gitea（并把 token 环境变量换成 `GITEA_TOKEN`）。构建单平台工件用共享脚本即可：

~~~bash
# 在 macOS 上补出 darwin 工件
GOOS=darwin GOARCH=arm64 bash opencode-go/scripts/build-archives.sh
~~~

Token 只从环境变量或 `--token-file` 读取，不接受命令行明文，避免进入 shell 历史与进程列表。查找顺序为 `GITEA_TOKEN` → `GITHUB_TOKEN` → `GH_TOKEN`，可用 `--token-env` 指定。

常用开关：

| 开关 | 说明 |
| --- | --- |
| `--api-base` | 覆盖 REST API 基址。默认 `github.com` → `https://api.github.com`，其他宿主 → `{实例}/api/v1`（GitHub Enterprise 可指向 `https://ghe.example.com/api/v3`）。 |
| `--dry-run` | 只打印计划与 SHA-256，不访问宿主。 |
| `--replace` | 附件已存在时先删除再上传（重复发布会话）。 |
| `--registry <path>` | 就地更新 `registry.json`，保留已手写的名称/描述/标签。 |
| `--skip-download-verify` | 跳过发布后的回下载校验（不建议）。 |

脚本会从文件名解析 `goos`/`goarch`，并强制校验 `<id>`、`--version` 与文件名一致——命名不符合商店约定的归档会直接报错，从源头避免商店无法安装。

### 切换发布宿主

`registry.json` 里每个插件的 `repository` 字段决定 Release URL 前缀。切到 Gitea 时把它改成 Gitea 仓库地址，重新发布一次让脚本写入真实 SHA-256 即可：

~~~json
{
  "repository": "https://gitea.example.com/zhonxinya/cpa-plugin",
  "install": {
    "type": "direct",
    "artifacts": [
      {
        "goos": "linux",
        "goarch": "amd64",
        "url": "https://gitea.example.com/zhonxinya/cpa-plugin/releases/download/v0.1.0/opencode-go_0.1.0_linux_amd64.zip",
        "sha256": "<发布脚本输出的 64 位十六进制>"
      }
    ]
  }
}
~~~

> 切换宿主会同时改变 `repository` 与 `homepage`。旧宿主的 Release 仍可下载，但商店会按新地址取件，因此必须在新宿主重新发布一遍，让 `registry.json` 里写入真实的 URL 与 SHA-256。

> 私有仓库的 Release 附件需要携带 Token 才能下载，而 CPA 插件商店不带凭据。要让商店直接安装，仓库必须公开可读。

### 其他发布途径（Gitea）

如果暂时不想上 CI，也可以用 Gitea 自带工具发布：

- **Web UI**：仓库 → Releases → **New Release**，填写 Tag `v0.1.0` 后手动上传 zip 与 `.sha256`（手动上传的 SHA-256 必须与文件一致，可用 `sha256sum` 核对）。
- **tea CLI**：`tea release create --tag v0.1.0 --asset dist/opencode-go_0.1.0_linux_amd64.zip --asset dist/opencode-go_0.1.0_linux_amd64.zip.sha256`。
- **REST API**：`POST /api/v1/repos/{owner}/{repo}/releases` 建 Release，再 `POST /api/v1/repos/{owner}/{repo}/releases/{id}/assets?name=<文件名>` 以 `application/octet-stream` 上传——这正是发布脚本内部使用的两条接口。

无论走哪条路，最后都要保证 `registry.json` 的 URL 与 SHA-256 和实际附件一致，可用下面的命令验证：

~~~bash
python3 scripts/validate-registry.py registry.json
python3 -m unittest discover -s tests -p 'test_*.py' -v
~~~
