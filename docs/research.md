# Bedrock Mantle 调研

调研日期：2026-10-10。实现前读取 AWS、OpenAI、shadcn/ui 官方文档，未使用社区代理代码作为协议依据。

## 结论与实现选择

| 调研结果 | 实现 |
| --- | --- |
| Mantle 原生提供 OpenAI 风格的 Chat Completions、Responses 和 Models | 直接转发 JSON 与 SSE，不转换为 Bedrock Converse |
| Mantle 推理端点常用 `/v1`，部分模型要求 `/openai/v1`；模型列表为 GET `/v1/models` | 推理使用配置的完整 Base URL，模型发现与连接检测自动去掉 `/openai`，使用同一上游的 `/v1/models` |
| 上游可使用 Bedrock API Key 或 AWS 凭据 | Bearer 与 AWS SDK SigV4 两种模式 |
| Responses 支持服务端状态，Project 和区域影响历史记录访问 | 维护持久化上游绑定，避免对历史 Response 随机分流 |
| 上游存储的 Responses 有保留期限 | 路由绑定按 30 天过期；正文保留由上游 `store` 参数决定 |
| Mantle 各模型支持的请求参数、工具和 API 不完全相同 | 保留未知字段、工具调用、多模态内容和 SSE 事件，让上游校验 |
| OpenAI Streaming 为 SSE，事件可跨网络块 | 原始字节转发，增量观察仅提取 ID、usage 和终态 |
| shadcn/ui 提供可放入项目的组件源码 | 使用官方 new-york registry 的 Button、Dialog、Input、Switch 等源码 |

## 范围说明

AWS 当前文档将 `bedrock-runtime` 推荐为一般新应用入口，将 Mantle 定位为保留兼容性以及特定能力的入口。本项目按需求以 **Mantle** 为目标。

两个入口并非完全等价：例如当前文档明确 `bedrock-runtime` 不提供 OpenAI 兼容 Models API，Responses 的 background、Project 等支持也不同。因此没有在后端偷偷替换端点或模型；管理员需要按 AWS 模型卡片选择实际地址。

这里的「OpenAI 兼容」指已列出的 HTTP 接口、请求字段与 SSE 行为，不代表实现 OpenAI 全平台 API，也不代表 Mantle 支持所有 OpenAI 参数。

## 真实联调发现

使用本机提供的 Bedrock Bearer 凭据对 `us-west-2` 的 `openai.gpt-oss-20b` 进行了真实调用。该模型的 Chat SSE 可以在发送带 `finish_reason` 的终态 chunk 后直接 EOF，不发送 `[DONE]`。实现保留已有事件，仅在所有请求的 choices 完成且正常 EOF 时补充标准 `[DONE]`；有 choice 未结束或传输失败时仍报告流中断。这一行为同时加入了多 choice 回归测试。

## 官方来源

1. AWS Responses / Mantle：
   https://docs.aws.amazon.com/bedrock/latest/userguide/bedrock-mantle.html
2. AWS Chat Completions：
   https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html
3. AWS endpoint comparison：
   https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html
4. AWS Projects：
   https://docs.aws.amazon.com/bedrock/latest/userguide/projects.html
5. AWS Bedrock API Key 使用：
   https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys-use.html
6. OpenAI Chat Completions create：
   https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create
7. OpenAI Responses create：
   https://developers.openai.com/api/reference/resources/responses/methods/create
8. shadcn/ui Vite 集成：
   https://ui.shadcn.com/docs/installation/vite
9. shadcn/ui Button registry（其余组件同目录）：
   https://ui.shadcn.com/r/styles/new-york/button.json

真实 Bearer 联调覆盖的区域和模型有限；其他模型、区域、IAM 策略仍需按部署环境验证。SigV4 本地测试覆盖签名构造和转发，尚未使用真实 AWS Access Key 做签名联调。
