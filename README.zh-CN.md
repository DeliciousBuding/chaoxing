# Chaoxing

[English](README.md)

Chaoxing 是可自托管的学习通只读客户端，用于查询课程、作业、考试、通知、课表和课程资料。一个 Go 二进制提供带 OAuth 认证的 HTTP MCP 接口和本地 JSON 命令。

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
| `query` | 使用配置中的上游客户端直接执行只读查询 |
| `configure` | 初始化加密凭据和独立的 owner 密码哈希 |
| `version` | 输出构建版本 |

八个工具为 `get_connection_status`、`list_courses`、`list_assignments`、`get_assignment`、`list_exams`、`list_notices`、`get_schedule` 和 `list_materials`。核心工具不提交作业、不作答考试、不签到、不修改学习进度。认证会更新会话状态；部分只读接口使用 HTTP POST，但不会修改课程内容。

owner OAuth 授权码有效期为 60 秒，access token 为 15 分钟，refresh token 为 30 天。token 为 opaque 随机值，私有文件状态中只持久化其哈希标识，不依赖数据库，也不使用 JWT 或 JWKS。

上游登录通过 HTTPS 使用学习通 AES 登录协议。GET 辅助函数检测到会话过期时，自动重新认证一次并重试读取一次；需要验证时由用户在官方网站完成。上游凭据和持久化 cookie 使用 AES-GCM 静态加密，密钥单独存放并限制为 root 可读。能读取密钥的 root 或进程仍可解密。owner OAuth 密码只保存加盐哈希，与需要恢复用于登录的上游密码分开。

Cloudflare Tunnel 只将 `127.0.0.1:8792` 发布到 `mcp.example.com` 一类 HTTPS 域名；私有 CLI 接口 `127.0.0.1:8793` 不接 Tunnel、不对公网开放。详见[架构](docs/architecture.md)与[安全政策](SECURITY.md)。

## 开发与许可

[CONTRIBUTING.md](CONTRIBUTING.md)说明离线检查与贡献范围，[CHANGELOG.md](CHANGELOG.md)记录未发布变更。

本项目采用 [Apache-2.0](LICENSE) 许可。[DanMo661/Xuexitong-mcp](https://github.com/DanMo661/Xuexitong-mcp) 与 [menghuanshiguang/chaoxing-cli](https://github.com/menghuanshiguang/chaoxing-cli) 均采用 MIT 许可，仅作为协议参考，没有复制其源代码。详见[来源说明](docs/references.md)。
