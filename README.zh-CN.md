# Chaoxing

[English](README.md)

Chaoxing 是面向 Agent 的可自托管学习通控制面，用于查询课程、作业、考试、通知、课表、课程资料，执行显式授权的课堂签到、抢答、答案生成与作业提交，管理本地答题草稿，并维护只存私有数据库的位置预设与课程绑定。一个 Go 二进制同时提供带 OAuth 认证的 HTTP MCP 接口、内嵌 React 的所有者管理界面，以及带 bearer 认证的本地 JSON API。Agent 和浏览器所有者可以发现能力、读取课程状态、提交按 scope 授权的动作、编辑本地答案草稿，但拿不到任意 URL、命令执行或文件系统权限。

## Agent-first 控制面

- MCP 客户端通过 HTTPS Tunnel 连接 `/mcp`，与本地 CLI 共用同一份经过校验的工具目录。
- 浏览器所有者访问 `/`，使用独立且绑定来源地址的 cookie 会话、CSRF 双提交和同一 operation allowlist 操作内嵌管理平台。
- 本机 loopback API 监听 8793，一次请求一个 JSON 工具调用，适合主机本地自动化。
- `get_capabilities` 和 `schema <operation>` 在接触账号数据前暴露操作 ID、scope、输入输出 schema。
- MCP、本地 API 和直接查询共用 `internal/operation` 的唯一契约，不允许适配层定义平行参数。
- 账号操作有队列与预算，上游 URL 全部 allowlist，调用方不能传任意 URL、shell 或路径。

### 远程控制模型

一个 daemon 独占上游会话、缓存和 watcher 状态。Agent 根据信任边界选择接口，但所有接口最终都进入同一份校验后的 operation：

| 接口 | 使用者 | 认证 | 边界 |
| --- | --- | --- | --- |
| `/` 与 `/api/*` | 人类所有者 | PBKDF2 owner 密码、`__Host-` 会话 cookie、CSRF | 只经 HTTPS 发布；不提供任意 URL/文件/命令能力 |
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
| `chaoxing.answer` | 通过配置的 provider 生成作业答案，并参与课堂抢答 |
| `chaoxing.work.submit` | 将已准备答案暂存或最终提交到上游 |
| `chaoxing.upload` | 从私有 uploads 目录上传预置文件 |
| `chaoxing.face` | 注册并使用账号人脸模板完成人脸签到 |

不提供泛化的 `write` scope。refresh token 只能收窄权限，不能扩大。上游密码和 cookie 使用 AES-GCM 加密，密钥单独存放；owner OAuth 密码只保存加盐 PBKDF2 哈希。详见[安全政策](SECURITY.md)。

安全设计按层收口：

- 授权按 operation scope 判断，并发生在触碰上游状态或本地草稿存储之前。
- 上游路由、请求体和响应大小都由 operation registry 与传输层限定。
- 工具契约不返回原始上游 body、凭据、cookie、OAuth token 或验证码 challenge 密钥。
- 同一账号的上游操作串行执行；进程级出站预算和公开 MCP admission 都有明确饱和行为。
- 每个账号拥有独立凭据、cookie、缓存、OAuth 状态、watcher 状态和通知目的地；只有可信传输健康状态共享。
- 内嵌 Web UI 不包含任何分析或 tracker，API 仅同源并强制配置 Host；可信代理才允许解析 `X-Forwarded-For`，会话绑定 IPv4 `/24` / IPv6 `/64` 客户端网络。
- 可选百度地图页是唯一的显式第三方脚本，且只在配置后进入位置页时加载。

全局作业 feed 仍不完整。按课程名查询会通过带签名的作业列表扫描匹配班级，分页结果与上游总数一致时可以返回 `complete: true`。这一标记只确认匹配班级的覆盖，不代表全账户完整。全局待交列表为空，不能证明所有作业都已提交。详见[工具与覆盖范围](docs/tools.md)。

## 构建与使用

使用 `go.mod` 要求的 Go 1.26.9 或 1.26 系列中更新的受维护补丁版本构建：

```sh
go build -o chaoxing ./cmd/chaoxing
./chaoxing version
```

Tag 发布工作流构建 Linux arm64、Linux amd64 和 Windows amd64 二进制，并生成 `SHA256SUMS`。校验和用于检查文件损坏，不是独立签名。安装时将对应二进制命名为 `chaoxing`（Windows 为 `chaoxing.exe`），Linux 下赋予执行权限。

使用私有 JSON stdin 初始化配置，然后运行 `chaoxing serve`。[配置与 Tunnel](docs/configuration.md)说明输入字段、文件权限及监听边界。凭据不放在命令参数或公开文件中。

管理台可选启用百度地图选点：坐标按 BD-09 存在私有 SQLite，地图脚本只在进入位置页时加载，地址搜索由后端代理。生产二进制已内嵌 Web UI。如果修改了 `web/src`，先重建前端资源再构建 Go：

```sh
npm --prefix web run build
go build -o chaoxing ./cmd/chaoxing
```

在已配置并运行服务的本机，通过 stdin 发送工具请求：

```sh
printf '%s\n' '{"tool":"get_connection_status","arguments":{}}' | chaoxing call
```

## 接口与认证

| 命令 | 用途 |
| --- | --- |
| `serve` | 管理上游会话与缓存，提供 Web UI、公开 MCP 和私有 loopback 调用 |
| `call` | 将一个 JSON 请求发往运行中的 daemon 私有接口 |
| `query` | 使用配置中的上游客户端直接执行有界查询 |
| `configure` | 初始化加密凭据和独立的 owner 密码哈希 |
| `version` | 输出构建版本 |

当前提供 32 个已验证工具，包括有界学业总览、行动项、学业摘要、抢答、答案生成、全局作业题目桥接、课堂 H5 证据采集、作业保存/提交、上传和人脸签到。`chaoxing capabilities` 查看能力和未支持范围，`chaoxing schema assignments.list` 查看契约。

可选的课堂活动监视器会按课程时间轮询指定班级，合并课程群消息里的签到卡片，发送运维告警，并自动提交普通、位置，以及配置了签到码的密码/手势签到；也可用 `auto_answer` 对课堂抢答执行一次性抢占。内置班级预设坐标、私有位置绑定、持久化设备身份、轮询抖动和单次尝试状态。空闲时段最多五分钟低频轮询并在课表边界唤醒；每个 tick 有 panic 隔离与 Gotify/通知通道告警，`/healthz` 输出聚合的 `last_tick_at` / `consecutive_panics` / `consecutive_crashes` 等安全指标；procd 异常退出会持久化计数并发送高优先级通知。上游返回滑块验证码时，若配置了 solver，`sign_activity` 会自动完成一次有界识别与重试；未配置时 watcher 只告警一次，Agent 可人工完成手动链路；二维码签到接收运营者扫到的原始 payload；拍照签到使用路径受限上传后的 object ID；人脸签到使用独立 scope 授权的人脸模板。

滑块识别通过 `captcha_solver` 配置。内置 local solver 使用背景图和缺口图且不访问网络；打码狗只接收背景图，key 保持私有。详见[配置文档](docs/configuration.md)。

通知可同时发送到飞书 webhook、ntfy 和 Gotify。watch 状态原子写入、私有保存，并与上游凭据分离。每个租户账号可以运行独立 watcher，拥有独立班级、课表、坐标、签到码、状态文件和通知目的地。

通知层是单向 provider fan-out：

| 通道 | 必需配置 | 投递方式 |
| --- | --- | --- |
| 飞书 | `feishu_webhook` | JSON text card；非零 provider acknowledgement 视为失败 |
| ntfy | `ntfy_url` + `ntfy_topic` | `POST <topic>`，携带 title 与按严重程度 5–9 的 priority |
| Gotify | `gotify_url` + `gotify_token` | `POST /message`，携带 `X-Gotify-Key` 与按严重程度 5–9 的 priority |

启用多个通道时各自独立投递，一个 provider 故障不会压制其他通道。watcher 启用但没有任何完整通道时直接启动失败，而不是静默丢告警。通知只携带运营者可读的活动身份和结果；凭据和签到码不会进入通知或 watch 状态。

答题链路按分阶段、可审计设计：

```text
list_assignments -> get_assignment_questions -> answer_work/upsert_work_draft -> save_work/submit_work

或对章节任务：

list_work_jobs -> get_work_questions -> answer_work/upsert_work_draft -> save_work/submit_work
```

草稿存储先用当前上游题目页校验 question_id，并原子持久化手动或 provider 生成的答案。`answer_work` 不提交；`save_work` 暂存；`submit_work` 要求所有题目都有草稿后才执行不可撤回的最终提交。

```sh
chaoxing assignments list --course-id <课程列表返回的ID> --status pending --json
chaoxing assignments get <作业列表返回的ID> --json
chaoxing study overview --course-id <课程列表返回的ID> --json
chaoxing study digest --course-id <课程列表返回的ID> --json
```

业务命令调用运行中的 daemon，共用会话。默认 JSON；成功数据写 stdout，完全失败的结构化错误写 stderr；覆盖不完整时保留可用数据，并返回退出码 3。`data.meta.status` 表示查询覆盖，不表示作业已完成。群消息、信箱与任务点暂不承诺支持，以能力目录为准。

owner OAuth 授权码有效期为 60 秒，随机 opaque access token 为 15 分钟，refresh family 为 30 天。refresh proof 认证族、计数器与随机值，以有界状态识别重放。`chaoxing.read` 用于读取；签到额外要求 `chaoxing.sign`；本地答案草稿编辑要求 `chaoxing.work`。不会持久化原始 access/refresh token，授权服务保持文件化状态，不用 JWT 或 JWKS；嵌入式 SQLite 仅保存可选出口健康、账号粘性绑定和脱敏活动捕获。默认启用 DCR，CIMD 需显式开启。可选的账号粘性 SOCKS5 出口和 `/t/<id>/` 下的多租户隔离见[配置说明](docs/configuration.md)，默认均关闭。

上游登录通过 HTTPS 使用学习通 AES 登录协议。GET 辅助函数检测到会话过期时，自动重新认证一次并重试读取一次；需要验证时由用户在官方网站完成。上游凭据和持久化 cookie 使用 AES-GCM 静态加密，密钥单独存放并限制为 root 可读。能读取密钥的 root 或进程仍可解密。owner OAuth 密码只保存加盐哈希，与需要恢复用于登录的上游密码分开。

Cloudflare Tunnel 只将 `127.0.0.1:8792` 发布到 `mcp.example.com` 一类 HTTPS 域名；私有 CLI 接口 `127.0.0.1:8793` 不接 Tunnel、不对公网开放。详见[架构](docs/architecture.md)与[安全政策](SECURITY.md)。

## 开发与许可

[CONTRIBUTING.md](CONTRIBUTING.md)说明离线检查与贡献范围，[CHANGELOG.md](CHANGELOG.md)记录未发布变更。

本项目采用 [Apache-2.0](LICENSE) 许可。[DanMo661/Xuexitong-mcp](https://github.com/DanMo661/Xuexitong-mcp) 与 [menghuanshiguang/chaoxing-cli](https://github.com/menghuanshiguang/chaoxing-cli) 均采用 MIT 许可，仅作为协议参考，没有复制其源代码。详见[来源说明](docs/references.md)。
