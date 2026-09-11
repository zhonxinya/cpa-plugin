# OpenCode Go CPA 插件

CLIProxyAPI (CPA) 原生插件，把 OpenCode Go 订阅接入 CPA：作为模型 **provider** 转发推理请求，并在管理面板展示订阅 **usage**（滚动 5 小时 / 每周 / 每月额度）。

插件以 C 共享库形式加载，provider key 为 `opencode-go`。

## 功能

- **模型 provider** — 注册 `opencode-go` 提供方；模型目录优先通过 `GET /models` 实时拉取并缓存（TTL 1 小时），拉取失败时回退到内置目录。
- **认证 provider** — 支持 API Key，来源包括配置文件环境变量、管理面板保存、以及 CPA 认证文件；API Key 不会过期，`auth.refresh` 只回读不改写。
- **执行器（executor）** — 透传 `chat/completions`、`responses`、`anthropic messages` 三种协议到 OpenCode Go，请求体保持客户端协议不变。
- **订阅用量** — 调用官方 `GET /usage`，把响应解析为滚动 / 每周 / 每月三个窗口，展示已用百分比、剩余百分比和重置时间。
- **管理面板** — 浏览器资源 `…/resource/plugins/opencode-go/status` 与需鉴权的 `…/management/plugins/opencode-go/status`，支持查看用量、添加账号（保存前用 `/usage` 校验一次）、删除账号。
- **安全边界** — 端点仅允许 `https://opencode.ai/zen/go/v1`（HTTPS、小写主机、无凭据/查询/片段）；凭据只以遮罩展示；未鉴权的资源路由绝不渲染账号数据；上游响应体有大小上限。

## 构建与测试

~~~bash
cd opencode-go
make test     # go test ./...
make vet      # go vet ./...
make package  # 构建 c-shared 动态库并打包 zip + sha256
~~~

`make package` 默认使用当前 `GOOS/GOARCH`；交叉构建可指定 `GOOS`/`GOARCH`。CI 用的是同一个逻辑的共享脚本 `scripts/build-archives.sh`（可从任意目录调用）：

~~~bash
GOOS=linux GOARCH=amd64 bash scripts/build-archives.sh
OUT_DIR=/tmp/dist GOOS=darwin GOARCH=arm64 bash scripts/build-archives.sh
~~~

## 发布

GitHub 与 Gitea 的 Release 附件下载地址与 REST API 形状完全一致（`{实例}/{owner}/{repo}/releases/download/{tag}/{文件名}`），因此同一个脚本 `../scripts/publish-release.py` 驱动两边，商店注册也不需要特殊处理。

### 自动发布（推荐）

推送 `v<VERSION>` 标签即触发 CI（`VERSION` 文件必须与标签一致，否则流水线直接失败）：

~~~bash
git tag v0.1.0
git push origin v0.1.0
~~~

| 宿主 | 工作流 | 覆盖平台 |
| --- | --- | --- |
| GitHub | `.github/workflows/release-opencode-go.yml` | `darwin/arm64`、`linux/amd64`、`linux/arm64` |
| Gitea | `.gitea/workflows/release-opencode-go.yml` | `linux/amd64`、`linux/arm64` |

流水线会跑测试、构建打包并自校验、上传 Release、**回下载比对 SHA-256**，最后用真实校验和更新 `registry.json`。

Gitea 侧需要额外准备：仓库 Secret `RELEASE_TOKEN`（Personal Access Token，作用域含 `repository` 读写），并开启 Actions 与注册 Runner。GitHub 侧使用自动注入的 `GITHUB_TOKEN`，无需配置。

### 本地手工发布

任何平台都可以用同一个脚本发布已构建好的工件（无需 CI）：

~~~bash
make package GOOS=darwin GOARCH=arm64
GITHUB_TOKEN=... python3 ../scripts/publish-release.py \
  --api-url https://github.com \
  --repo zhonxinya/cpa-plugin \
  --tag v0.1.0 \
  --plugin-id opencode-go \
  --version 0.1.0 \
  --asset dist/opencode-go_0.1.0_darwin_arm64.zip \
  --registry ../registry.json
~~~

把 `--api-url` 换成 Gitea 实例地址并把 token 换成 `GITEA_TOKEN` 即发布到 Gitea。脚本会拒绝文件名不符合 `<id>_<version>_<goos>_<goarch>.zip` 的归档，并在发布后重新下载附件比对 SHA-256。完整的发布与迁移说明见[仓库根 README](../README.md#发布github--gitea)。

## 配置

在 CPA 配置的 `plugins.configs.opencode-go` 下配置：

~~~yaml
plugins:
  enabled: true
  configs:
    opencode-go:
      enabled: true
      priority: 1
      timeout: 15s
      endpoint: https://opencode.ai/zen/go/v1   # 可选，必须是该值
      accounts:
        - id: go-main
          label: OpenCode Go 主账号
          plan: go
          api_key_env: OPENCODE_GO_API_KEY
~~~

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `enabled` | bool | 是否启用，默认 `true`。 |
| `priority` | int | 插件优先级，默认 `1`。 |
| `timeout` | string | 单次用量/目录请求超时，如 `15s`，必须是正数时长。 |
| `endpoint` | string | 端点覆盖，只允许 `https://opencode.ai/zen/go/v1`。 |
| `accounts` | array | 账号列表；`plans` 为旧别名，语义相同。 |

每个账号字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | ASCII 字母/数字/`.`/`_`/`-`，最长 128，且不可重复。 |
| `api_key_env` | 是 | 存放 API Key 的环境变量名，解析结果不能为空。 |
| `label` | 否 | 展示名称，缺省用 `id`。 |
| `plan` | 否 | 默认 `go`。 |
| `endpoint` | 否 | 账号级端点覆盖，同样只允许上述地址。 |
| `disabled` | 否 | 禁用后不刷新用量，但仍显示在面板。 |

面板保存的账号写入 CPA 认证目录（`CLIPROXY_AUTH_DIR`，未设置时用当前工作目录）下的私有文件 `.opencode-go-accounts`，权限 `0600`。

## 订阅用量接口

~~~http
GET https://opencode.ai/zen/go/v1/usage
Authorization: Bearer <API_KEY>
Accept: application/json
~~~

响应示例（已脱敏）：

~~~json
{
  "usage": {
    "rolling": { "status": "ok", "percent": 2, "resetsAt": "2026-09-11T08:11:15.128Z" },
    "weekly":  { "status": "ok", "percent": 6, "resetsAt": "2026-09-14T00:00:00.128Z" },
    "monthly": { "status": "ok", "percent": 3, "resetsAt": "2026-10-10T10:00:50.128Z" }
  }
}
~~~

- `percent` 为已用百分比，剩余百分比 = `100 - percent`，并被限制在 `[0, 100]`。
- 窗口映射：`rolling → five_hour`、`weekly → weekly`、`monthly → monthly`。
- `status` 为 `rate-limited` 时面板显示“已达上限”。
- 状态码会被翻译为明确错误：`401` 凭据被拒、`403` 未订阅、`429` 限流、`404` 接口不存在，其余透传状态码。

## 模型与执行器

| 客户端协议 | 上游路径 |
| --- | --- |
| `chat-completions` | `POST /chat/completions` |
| `responses` / `openai-response` | `POST /responses` |
| `anthropic` / `claude` | `POST /messages` |

模型目录来自 `GET /models`（OpenAI 兼容的 `{"data":[...]}`），去重并过滤非法 id。执行请求会剥离 hop-by-hop 头，注入 `Authorization: Bearer <API_KEY>`，并在缺少会话头时生成稳定的 `x-opencode-session`。`429` 会作为可重试错误返回给 CPA。

## 管理接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v0/management/plugins/opencode-go/status?view=overview\|accounts&refresh=<id\|all>` | 用量看板 / 账号管理表。 |
| POST | `/v0/management/plugins/opencode-go/accounts` | 校验并保存账号（`label`、`credential`、可选 `endpoint`）。 |
| DELETE | `/v0/management/plugins/opencode-go/accounts?id=<id>` | 删除已保存账号。 |
| GET | `/v0/resource/plugins/opencode-go/status` | 浏览器资源外壳，不包含任何账号数据。 |

## 测试

~~~bash
cd opencode-go
go test ./...
~~~

测试覆盖用量解析、模型目录、HTTP 客户端错误映射、服务刷新、账号存储、面板渲染、provider 注册 / 模型 / 认证解析 / 执行器与管理接口。
