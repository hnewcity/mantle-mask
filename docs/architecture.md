# 架构与协议边界

```mermaid
flowchart LR
    UI[React + Vite + shadcn/ui] -->|管理令牌| Admin[/api/admin]
    App[OpenAI SDK / 应用] -->|网关 API Key| API[/v1]
    Admin --> Store[配置、用户与加密凭据]
    UI -->|用户 Key| Self[/api/me]
    Self --> Usage[(SQLite 用量库)]
    Admin --> Usage
    Admin --> Playground[Playground]
    Playground --> Router
    API --> Router[模型过滤 / Responses 绑定 / 加权轮询]
    Store --> Router
    Router --> Auth[Bedrock Bearer 或 SigV4]
    Auth --> A[Mantle 上游 A]
    Auth --> B[Mantle 上游 B]
    A --> Stream[原始 JSON / SSE]
    B --> Stream
    Stream --> Meta[usage / 元数据 / Response ID]
    Meta --> Usage
    Meta --> Affinity[持久化绑定索引]
    Stream --> App
```

## 目录

```text
cmd/mantle-mask/          进程入口、环境变量、优雅退出
internal/config/         配置事务、版本迁移、AES-GCM、用户与客户端密钥
internal/usage/          SQLite / MySQL 请求记录、用量补记、分组聚合
internal/gateway/        上游 HTTP 客户端、SigV4、路由、SSE、统计
internal/server/         Gin 路由、管理与用户 API、鉴权中间件、CORS、静态前端
web/src/components/ui/   shadcn/ui 官方组件源码
web/src/pages/           管理控制台与用户用量门户
web/src/lib/             API 客户端、SSE 解析器
scripts/                 WSL 开发工具与隔离 E2E 上游
```

## 请求过程

HTTP 层使用 Gin，分别注册 `/api/admin`、`/api/me` 和 `/v1` 路由组，以中间件校验管理员令牌或用户 Key。全局中间件处理安全响应头、CORS 和异常恢复；未知 API 路径也先鉴权，再返回原有 JSON 错误，不落入前端 SPA。自动路径纠正和尾斜杠重定向关闭。

进程继续使用 `http.Server` 管理超时和优雅退出；Gin 将原始 `Request` 传给网关，响应 writer 适配层保留 FlushError、Unwrap 和 Gin 的状态记录，确保 SSE 字节转发、写入截止时间、断连错误及客户端取消继续传递。

1. 校验客户端 Bearer 密钥的 SHA-256 摘要、撤销状态及归属用户启用状态，建立用户和 Key 身份。新推理请求先写入 SQLite，失败则返回 503。
2. 检查方法、路由、JSON 大小与基础字段，其他字段以 `json.RawMessage` 保留，避免大整数精度损失。
3. 如果是历史 Responses，按客户端身份和 Project 查找绑定；未知 ID 返回 404，目标配置已改变返回 409。
4. 新请求过滤禁用上游、模型映射和冷却状态，进行平滑加权轮询。
5. 改写该上游的模型别名，选择 Project，重新设置上游鉴权。只转发允许的请求头，不转发客户端的 Authorization、Cookie 或任意管理头。
6. 响应前的临时错误可以尝试其他未使用上游。设置了 Responses 绑定或流已开始时禁止切换。
7. 返回原始正文／SSE。观察器提取 Response ID 后先持久化再发送包含该 ID 的数据块，避免客户端收到无法恢复路由的 ID。
8. 记录最终结果和上游 usage。流中断时发送 SSE 错误事件，日志记为失败；已经发出的 HTTP 状态无法修改。

## 状态与隔离

- 配置：v2 版本化 JSON，v1 升级时自动把旧 Key 归入默认用户；写临时文件、fsync、rename，成功后才替换内存快照。
- 上游凭据：AES-256-GCM，随机 nonce，以不可变上游 ID 作为 additional authenticated data。
- 客户端密钥：256 位随机 token，仅保留摘要、短前缀、名称、归属用户、创建时间。撤销时清除摘要并保留撤销时间与归属，统计不丢失。
- 管理令牌：来自环境或私有本地文件；服务端常量时间比较摘要。浏览器仅保存在 sessionStorage。
- Responses：对客户端密钥 ID、原始 Project 请求头和 Response ID 组成的作用域做哈希；内容为上游 ID、配置指纹、有效 Project、模型和到期时间。不会储存对话正文。
- Response 路由索引最多 100000 条未过期绑定；写入时清理到期记录，达到上限返回持久化错误。
- 查询 Models 的远端结果缓存 60 秒，键包含上游、地址、凭据和 Project。存在部分失败时返回可用模型，并带 `X-Mantle-Partial: true`。
- 最近日志最大 100–10000 条，总览仍维护本次启动累计值。历史用量独立写入 SQLite，包含请求时间、归属、模型、状态与 Token，不记录正文。
- SQLite 使用 WAL、synchronous=FULL、单连接池；写入有超时，报表通过单个读事务生成一致快照。pure-Go modernc 驱动支持 CGO_ENABLED=0 构建。
- 新推理在接触上游前保存请求和尝试次数，返回 usage 前持久化计数；计数按同一请求的最大快照更新，重复终态不会累加。Responses 作用域关联原请求，后台结果查询可补记原请求。
- 用户报表在服务端强制设置 user_id 过滤，再应用日期、Key、模型参数；不返回其他用户或上游配置。缓存／推理 Token 作为子项展示，总量只使用输入 + 输出。

当前存储面向单进程。需要多副本时，应把配置和绑定迁入事务数据库，并把调度状态、健康信息、限流和审计做成共享服务。

## 错误、取消与连接

- 本地错误采用 `{"error":{"message":...,"type":...,"param":null,"code":...}}`。
- 上游已经采用 OpenAI 错误形状时保留错误内容与 HTTP 状态，并尝试去除当前配置中的凭据字面量；其他上游错误转换为统一形状。
- 仅选择允许的上游响应头；不接受上游 Set-Cookie、CORS 或重定向地址。HTTP 客户端禁止跟随重定向。
- 客户端断开、主动取消和请求期限通过 Go context 传到上游；流写入另设截止时间，避免慢客户端无限占用连接。
- 响应中的模型名称保持上游值，工具调用参数和 SSE event/data/comment 边界不重建。Mantle Chat 在所有 choices 带 finish_reason 结束后正常 EOF、且没有 `[DONE]` 时，补充一个标准结束 sentinel。
- SSE 单行／事件最多观察 1 MiB；超过限制终止并发送错误事件，避免为日志解析保留无限内存。

## 前端行为

- 上游配置与密钥操作立即请求后端并刷新状态。
- 模型连接检测只执行 GET `/v1/models`，不发送计费推理；即使推理 Base URL 是 `/openai/v1`，模型发现仍使用同一上游的 `/v1/models`。路径在 SigV4 签名前规范化，原始推理地址和 Responses 绑定不变。
- Playground 走管理员专用入口，不向浏览器下发上游密钥；支持两种 API、流式开关、系统消息、额外 JSON 参数、停止请求和原始事件查看。
- Playground 默认发送完整本地对话历史；Responses 默认 `store:false`。API 客户端的 `store` 参数按原请求转发。
- 全局状态每 15 秒刷新；图表和表格没有固定的模拟业务数据。
