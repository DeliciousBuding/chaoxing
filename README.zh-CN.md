# Chaoxing

[English](README.md)

Chaoxing 是面向 Agent 的可自托管学习通控制面，用于查询课程、作业、考试、通知、课表、课程资料，执行显式授权的课堂签到，并管理手动答题草稿。一个 Go 二进制同时提供带 OAuth 认证的 HTTP MCP 接口和带 bearer 认证的本地 JSON API。Agent 可以发现能力、读取课程状态、提交按 scope 授权的签到、编辑本地答案草稿，但拿不到任意 URL、命令执行或文件系统权限。

## Agent-first 控制面

- MCP 客户端通过 HTTPS Tunnel 连接 `/mcp`，与本地 CLI 共用同一份经过校验的工具目录。
- 本机 loopback API 监听 8793，一次请求一个 JSON 工具调用，适合主机本地自动化。
- `get_capabilities` 和 `schema <operation>` 在接触账号数据前暴露操作 ID、scope、输入输出 schema。
- MCP、本地 API 和直接查询共用 `internal/operation` 的唯一契约，不允许适配层定义平行参数。
- 账号操作有队列与预算，上游 URL 全部 allowlist，调用方不能传任意 URL、shell 或路径。

### 远程控制模型

一个 daemon 独占上游会话、缓存和 watcher 状态。Agent 根据信任边界选择接口，但所有接口最终都进入同一份校验后的 operation：

| 接口 | 使用者 | 认证 | 边界 |
| --- | --- | --- | --- |
| `POST /mcp` | 远程 MCP 客户端 | owner OAuth 与 operation scope | 只经 HTTPS 发布；工具结果不含原始 token 或验证码密钥 |
| `POST /call` | 主机本地自动化 | 私有 bearer token | 仅 loopback 监听；绝不接 Tunnel |
| `chaoxing call` | 本地运维者或脚本 | 读取私有本地配置 | 每次一个 JSON 请求，走同一 operation registry |
| `chaoxing query` | 隔离诊断 | 私有配置 | 直接使用上游客户端；不要与 `serve` 并发 |

Agent 拿到的是完整课程控制面，不是通用 shell、任意 URL fetcher、文件浏览器或无限账号写入器。`get_capabilities` 是当前契约的事实源，包括明确不支持的领域；`schema <operation>` 在调用前描述精确输入输出。

| OAuth scope | 能力 |
| --- | --- |
| `chaoxing.read` | 课程、作业、考试、通知、课表、资料、章节、活动、题目读取 |
| `chaoxing.sign` | 普通、位置，以及带运营者签到码的密码/手势签到 |
| `chaoxing.work` | 创建、修改、查看、删除和导出本地答题草稿 |

不提供泛化的 `write` scope。refresh token 只能收窄权限，不能扩大。上游密码和 cookie 使用 AES-GCM 加密，密钥单独存放；owner OAuth 密码只保存加盐 PBKDF2 哈希。详见[安全政策](SECURITY.md)。

安全设计按层收口：

- 授权按 operation scope 判断，并发生在触碰上游状态或本地草稿存储之前。
- 上游路由、请求体和响应大小都由 operation registry 与传输层限定。
- 工具契约不返回原始上游 body、凭据、cookie、OAuth token 或验证码 challenge 密钥。
- 同一账号的上游操作串行执行；进程级出站预算和公开 MCP admission 都有明确饱和行为。
- 每个账号拥有独立凭据、cookie、缓存、OAuth 状态、watcher 状态和通知目的地；只有可信传输健康状态共享。

全局作业 feed 仍不完整。按课程名查询会通过带签名的作业列表扫描匹配班级，分页结果与上游总数一致时可以返回 `complete: true`。这一标记只确认匹配班级的覆盖，不代表全账户完整。全局待交列表为空，不能证明所有作业都已提交。详见[工具与覆盖范围](docs/tools.md)。

## 构建与使用

使用 `go.mod` 要求的 Go 1.26.9 或 1.26 系列中更新的受维护补丁版本构建：

```sh
go build -o chaoxing ./cmd/chaoxing
./chaoxing version
```

Tag 发布工作流构建 Linux arm64、Linux amd64 和 Windows amd64 二进制，并生成 `SHA256SUMS`。校验和用于检查文件损坏，不是独立签名。安装时将对应二进制命名为 `chaoxing`（Windows 为 `chaoxing.exe`），Linux 下赋予执行权限。

使用私有 JSON stdin 初始化配置，然后运行 `chaoxing serve`。[配置与 Tunnel](docs/configuration.md)说明输入字段、文件权限及监听边界。凭据不放在命令参数或公开文件中。

在已配置并运行服务的本机，通过 stdin 发送工具请求：

```sh
printf '%s\n' '{"tool":"get_connection_status","arguments":{}}' | chaoxing call
```

## 接口与认证

| 命令 | 用途 |
| --- | --- |
| `serve` | 管理上游会话与缓存，提供公开 MCP 和私有 loopback 调用 |
| `call` | 将一个 JSON 请求发往运行中的 daemon 私有接口 |
| `query` | 使用配置中的上游客户端直接执行有界查询 |
| `configure` | 初始化加密凭据和独立的 owner 密码哈希 |
| `version` | 输出构建版本 |

原有八个工具之外，增加有界学业总览、来源明确的行动项、有界学业摘要与能力发现。`chaoxing capabilities` 查看已验证能力和未支持范围，`chaoxing schema assignments.list` 查看契约。

可选的课堂活动监视器会按课程时间轮询指定班级，合并课程群消息里的签到卡片，发送运维告警，并自动提交普通、位置，以及配置了签到码的密码/手势签到。内置班级预设坐标、持久化设备身份、轮询抖动和单次尝试状态。上游返回滑块验证码时，若配置了 solver，`sign_activity` 会自动完成一次有界识别与重试；未配置时 watcher 只告警一次，Agent 可人工完成手动链路；二维码签到通过 `sign_activity` 接收运营者扫到的原始 payload；拍照和人脸仍只告警。

滑块识别是可选的 provider 边界，通过 `captcha_solver` 配置；daemon 只发送背景图，provider key 保持私有。详见[配置文档](docs/configuration.md)。

通知可同时发送到飞书 webhook、ntfy 和 Gotify。watch 状态原子写入、私有保存，并与上游凭据分离。每个租户账号可以运行独立 watcher，拥有独立班级、课表、坐标、签到码、状态文件和通知目的地。

通知层是单向 provider fan-out：

| 通道 | 必需配置 | 投递方式 |
| --- | --- | --- |
| 飞书 | `feishu_webhook` | JSON text card；非零 provider acknowledgement 视为失败 |
| ntfy | `ntfy_url` + `ntfy_topic` | `POST <topic>`，携带 title 与 priority 5 |
| Gotify | `gotify_url` + `gotify_token` | `POST /message`，携带 `X-Gotify-Key` 与 priority 5 |

启用多个通道时各自独立投递，一个 provider 故障不会压制其他通道。watcher 启用但没有任何完整通道时直接启动失败，而不是静默丢告警。通知只携带运营者可读的活动身份和结果；凭据和签到码不会进入通知或 watch 状态。

手动答题按本地优先设计：

```text
list_work_jobs -> get_work_questions -> upsert_work_draft -> export_work_draft
```

草稿存储会先用当前上游题目页校验 question_id，原子持久化答案，并导出 `pyFlag=1` 的暂存请求与最终提交请求两种 payload。服务不生成答案，也不会把 payload 发送到上游。

```sh
chaoxing assignments list --course-id <课程列表返回的ID> --status pending --json
chaoxing assignments get <作业列表返回的ID> --json
chaoxing study overview --course-id <课程列表返回的ID> --json
chaoxing study digest --course-id <课程列表返回的ID> --json
```

业务命令调用运行中的 daemon，共用会话。默认 JSON；成功数据写 stdout，完全失败的结构化错误写 stderr；覆盖不完整时保留可用数据，并返回退出码 3。`data.meta.status` 表示查询覆盖，不表示作业已完成。群消息、信箱与任务点暂不承诺支持，以能力目录为准。

owner OAuth 授权码有效期为 60 秒，随机 opaque access token 为 15 分钟，refresh family 为 30 天。refresh proof 认证族、计数器与随机值，以有界状态识别重放。`chaoxing.read` 用于读取；签到额外要求 `chaoxing.sign`；本地答案草稿编辑要求 `chaoxing.work`。不会持久化原始 access/refresh token，授权服务保持文件化状态，不用 JWT 或 JWKS；嵌入式 SQLite 仅用于可选代理池的健康持久化。默认启用 DCR，CIMD 需显式开启。可选的 SOCKS5 代理池（含源地址绑定、失败冷却与直连兜底）和 `/t/<id>/` 下的多租户隔离见[配置说明](docs/configuration.md)，默认均关闭。

上游登录通过 HTTPS 使用学习通 AES 登录协议。GET 辅助函数检测到会话过期时，自动重新认证一次并重试读取一次；需要验证时由用户在官方网站完成。上游凭据和持久化 cookie 使用 AES-GCM 静态加密，密钥单独存放并限制为 root 可读。能读取密钥的 root 或进程仍可解密。owner OAuth 密码只保存加盐哈希，与需要恢复用于登录的上游密码分开。

Cloudflare Tunnel 只将 `127.0.0.1:8792` 发布到 `mcp.example.com` 一类 HTTPS 域名；私有 CLI 接口 `127.0.0.1:8793` 不接 Tunnel、不对公网开放。详见[架构](docs/architecture.md)与[安全政策](SECURITY.md)。

## 开发与许可

[CONTRIBUTING.md](CONTRIBUTING.md)说明离线检查与贡献范围，[CHANGELOG.md](CHANGELOG.md)记录未发布变更。

本项目采用 [Apache-2.0](LICENSE) 许可。[DanMo661/Xuexitong-mcp](https://github.com/DanMo661/Xuexitong-mcp) 与 [menghuanshiguang/chaoxing-cli](https://github.com/menghuanshiguang/chaoxing-cli) 均采用 MIT 许可，仅作为协议参考，没有复制其源代码。详见[来源说明](docs/references.md)。
