# 验证记录

日期：2026-10-10。

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

- 当前机器没有 Docker，Dockerfile / Compose 尚未实际构建运行。
- 真实 SigV4 调用未执行；提供的凭据是 Bearer，SigV4 验证限于本地签名和转发测试。
- 没有遍历全部模型／区域／工具组合，也没有做生产负载压测或多副本部署测试。
