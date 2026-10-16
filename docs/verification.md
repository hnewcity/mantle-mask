# 验证记录

## 2026-10-16 · v0.3.1 用量报表性能

- 替换重复扫描与逐维度 JSON 价格关联，改为同一 WAL 快照单次流式读取、每请求定价及多维度汇总。无需 schema 迁移，不缓存过期费用，不修改接口字段。
- 保留 v0.3.0 SQL 作为测试专用独立基准；空库 / 合成历史、所有过滤器、正负时区、地区 / 别名 / 手动价格、长上下文边界、缓存上限、免费 / 缺失价格与用量状态逐字段一致。请求取消和计数溢出均释放 reader。
- `go test -race ./...`、`go vet ./...`、Vitest 24 项、TypeScript / 前端生产构建、Go 构建，以及 Playwright / SDK 全量 17 项通过。已扫描待发布补丁，无凭据泄漏。
- 2 万 / 10 万条历史请求基准运行通过，最终独立测量和复现命令见 [用量报表性能](usage-report-performance.md)。只使用临时数据库、合成价格及模拟上游，不读取生产数据库、不发起 AWS 推理。
- 发布到 GitHub / 容器仓库供管理员手动部署；此次未连接服务器部署或重启线上服务。

## 2026-10-14 · 参考价格同步与手动覆盖

- 同步 AWS 官方、LiteLLM、models.dev，统一百万 Token 美元单位；固定来源 URL、区域校验、白名单重定向和下载 / 超时 / 并发限制。不使用 AWS 凭据，不发起模型推理。
- 区域与当前模型别名精确匹配，手动覆盖优先且与缓存分开保存。失败保留上次缓存；缓存 / 推理不重复计价，可识别长上下文阶梯按单请求输入计价，不将缺失或冲突价格硬套为免费 / 基础价。
- `go test -race ./...`、`go vet ./...`、TypeScript、Vitest 24 项及前端 / Go 构建通过。Playwright / SDK 全量 17 项通过，覆盖同步 UI、来源失败、区域 / 别名、覆盖优先、CSV、个人权限和响应式布局；桌面 / 手机已视觉检查。
- 真实公开价格目录下载检查成功（AWS us-east-1 / us-west-2、LiteLLM、models.dev）；其余测试全部使用本地隔离数据库和合成价格 / 模拟上游，不依赖在线价格，不调用真实 AWS 推理。
- 此次提交基于 GitHub main，独立工作树验证；GitLab 的旧历史和原目录的其他改动不纳入本次提交。

## 2026-10-14 · 用量费用估算

- 管理端新增模型单价配置（USD / 百万 Token），个人页只读；精确匹配记录的模型 ID / 别名，不猜测官方价格。输入、输出及可选缓存输入单价持久化，并随完整备份迁移。
- 报告、全部分组、每日模型明细和 CSV 共享费用口径及筛选范围；缓存从普通输入中扣除，推理不重复计价。未配置价格与已配置免费价格、未返回用量保持区分，历史金额按当前配置重新估算。
- `go test -race ./...`、`go vet ./...`、Vitest 23 项、TypeScript 和前端／Go 生产构建通过。Windows 主机没有原生 Go，后端检查使用 WSL 中的 Go 容器；容器构建使用 `-buildvcs=false` 避免宿主 Git 所有权检查影响构建。
- Playwright / SDK 全量 16 项通过，新增覆盖单价编辑／重复 ID、所有分组金额、模型筛选、CSV、个人权限与手机布局；同时复测既有上游用量及在线备份行为。
- 仅使用本地隔离数据库和模拟上游；未读取或修改生产配置，未执行真实 AWS 推理。桌面和手机单价弹窗截图已完成视觉检查。

## 2026-10-14 · 在线迁移修订

- Go race 覆盖全部后端包并复测 gateway / server；`go vet` 通过。Vitest 20 项、TypeScript、前端生产构建通过。
- Playwright / SDK 全量 15 项通过，包括新增的持续 SSE 下在线导出、恢复及新推理请求验证。
- WAL 快照持有期间用量写入继续完成；空库 / 非空库都不会把快照之后的记录混进快照。Responses 绑定及用量关联在同一状态检查点捕获。
- 目标实例中的在途请求不会被导入覆盖；已鉴权请求保留原配置与凭据，旧请求的冷却状态不会污染恢复后的新凭据。
- 跨实例未完成记录计为「待补齐」，重启不伪造 499；完整快照补齐后清除此状态。反复导入以及再导入旧快照都不重复累加或降级最终用量。
- 仅用量补齐模式保留新建 Key、目标设置和目标新流量；错误请求归属冲突在写入前拒绝。写入失败的恢复仍可重启幂等续做，不再通过全局 503 停用流量。
- 在线备份使用 payload v2，兼容读取 v1 备份；用量库 schema v3 增加待补齐标记，旧二进制不能直接读取升级后的数据卷。
- 已检查桌面和 390px 手机截图，未发现横向溢出。验证均使用临时目录与模拟上游。

## 2026-10-14 · 完整备份初版（下面的停流量限制已由在线修订取消）

- `go test -race ./...`、`go vet ./...`：通过。
- `npm --prefix web test`：5 个文件、20 个用例通过；`npm --prefix web run typecheck` 通过。
- `npm --prefix web run build` 与 `go build -o bin/mantle-mask ./cmd/mantle-mask`：通过；Vite 仍提示主包超过 500 kB，非构建错误。
- `npm --prefix web run test:e2e`：14 个浏览器 / SDK 用例全部通过，包括新增备份流程。
- 新增覆盖：跨本地主密钥和外部主密钥迁移；保留原 Key、用户停用/Key 吊销状态、API Key 与 SigV4 上游凭据、设置、历史用量和 Responses 绑定；恢复及再次重启后原 Key 仍可用。
- 失败路径覆盖：未授权请求、短密码、错密码、损坏/截断文件、不支持版本、不完整或重复数据、缺少覆盖确认均不修改原数据；活动流式请求和并发备份返回冲突；SQLite 失败回滚；跨文件恢复失败后拒绝流量，重启自动续做。
- 已检查桌面和 390px 手机截图，备份区域无横向溢出；用隔离临时目录与模拟上游验证，没有迁移运行中的用户数据。

## 2026-10-12 · 开源发布验证

- `bash scripts/build.sh`：前端生产构建与 Go 二进制构建通过。
- `go test -race ./...`、`go vet ./...`：通过。
- Vitest：5 个测试文件、20 个用例通过；TypeScript 构建检查通过。
- 首页截图：隔离模拟数据生成，已人工检查；没有读取真实账号或生产数据。
- `docker build -t mantle-mask:smoke .`：Linux amd64 镜像构建通过。
- `bash scripts/docker-smoke.sh mantle-mask:smoke`：非 root 用户、嵌入前端、鉴权、可写数据卷和重启持久化检查通过。脚本在重启后重新读取 Docker 随机宿主机端口。
- `actionlint`、Compose 配置解析、shell 语法、文档本地链接检查：通过。
- 对待公开快照运行 Gitleaks：未发现泄漏。扫描结果不能替代后续凭据管理与人工审查。
- 首次 GitHub CI 已通过 Go / 前端 / 全部浏览器测试；完整发布结果以 [CI](https://github.com/hnewcity/mantle-mask/actions/workflows/ci.yml) 对应提交为准。
- Markdown 流式 UI 的进行中状态使用模拟上游的显式暂停 / 释放机制验证，不再依赖短暂的时间窗口。
- 本轮没有调用真实 AWS 推理。

## 2026-10-10 · 历史验证记录

## 环境

- Ubuntu WSL2，代码保留在当前 Windows 工作区。
- Go 1.27.2，Node.js 24.21.0。
- Gin 1.12.0 提供后端路由、分组鉴权和全局中间件。
- React 19、Vite 8、Tailwind CSS 4、官方 shadcn/ui new-york 组件源码。
- 依赖锁定在 `go.sum` 和 `web/package-lock.json`。

## 自动化验证

| 检查 | 结果 |
| --- | --- |
| Go 单元和 HTTP 集成测试，开启 race detector | 通过 |
| `go vet` | 通过 |
| TypeScript 检查和 Vite production build | 通过 |
| 前端 SSE 解析测试：UTF-8 分块、CRLF、多行 data、错误传播 | 通过 |
| Playwright 控制台完整流程 | 通过 |
| Playwright 用户创建、Key 分配、个人用量页、停用／撤销和 CSV 导出 | 通过 |
| 未修改的官方 OpenAI SDK Models / Chat / Responses / retrieve | 通过 |
| 390 px 移动布局无横向页面溢出 | 通过 |

后端用例覆盖：加密保存、配置重启和并发写入、密钥撤销、鉴权隔离、URL 校验、模型别名、大整数精度、权重分配、重试和冷却、SSE 字节保留及实时 flush、Responses 路由持久化与作用域、禁用历史请求的跨上游重试、聚合模型、SigV4 签名字段、禁止带凭据重定向、统一错误格式、流中断和客户端取消。

真实 Mantle 结束帧兼容有专门回归测试：单 choice 的 stop / tool_calls、多个 choices 全部结束，以及只有部分 choices 结束时不得补 `[DONE]`。

多用户与用量新增验证：v1 配置和旧 Key 无损迁移、用户停用／重新启用、撤销 Key 后保留归属、服务端强制用户查询范围、禁用用户 Key 无法推理或登录、SQLite 并发写入、重启后的未完成请求恢复、重复 usage 快照不累加、后台 Responses 在多次读取中补记、UTC+8 日期边界、真实零用量与未报告用量区分、流式 include_usage 自动添加和显式关闭、用量库不可写时阻止新推理。Go race 与 vet 均通过。

Gin 迁移新增验证：未知 API 路径仍先鉴权并返回 JSON 错误、尾斜杠与大小写不触发重定向、所有路由的 OPTIONS 预检先于鉴权、SPA GET／HEAD 与静态资源 Range 请求、管理 JSON 大小及未知字段限制、异常响应不包含请求内容、已开始的 SSE 在异常时中断连接、响应 writer 保留 Flush 和写入截止时间、Flush 错误按客户端断连记录。完整 Go race 与 vet、8 项前端单元测试、TypeScript／Vite 构建及 6 项 Playwright 端到端测试均通过。

`CGO_ENABLED=0` 的 Linux 二进制已成功构建并用于浏览器／真实 AWS 验证；SQLite 不依赖系统 C 编译器运行。

## 真实 AWS 联调

使用用户提供的环境文件，仅提取 Bedrock Bearer 和区域配置。凭据没有写入源码、报告或实际运行配置。

- 区域：`us-west-2`
- 模型：`openai.gpt-oss-20b`
- 模型发现：返回 49 个模型
- Chat Completions 非流式：通过，实际收到文本
- Chat Completions SSE：通过，实际收到文本和规范化结束事件
- Chat 流式默认请求 usage：通过，Mantle 实际返回 Token 用量
- Responses SSE：通过，收到 `response.completed`
- Responses 创建、读取、`previous_response_id` 多轮及省略模型的继承：通过
- 删除本次创建的两条云端 Responses：通过
- 测试临时网关和加密凭据目录：已清理
- 新增按用户计量核对：5 次推理，输入 385、输出 144、合计 529 Token；5 次均有 usage，与 SDK 接收值一致，读取／删除 Responses 没有新增推理用量
- 用户 Key 请求统计时即使传入其他 user_id，仍只返回本人用量

脱敏的本次详细结果位于未纳入版本控制的 `test-results/aws-smoke.json`。测试提示词为固定的短确认请求，没有使用用户业务数据。

## 尚未覆盖

- 2026-10-10 记录时尚未实际构建 Docker；2026-10-12 已补充本地构建和容器 smoke 验证，见上文。
- 真实 SigV4 调用未执行；提供的凭据是 Bearer，SigV4 验证限于本地签名和转发测试。
- 没有遍历全部模型／区域／工具组合，也没有做生产负载压测或多副本部署测试。
