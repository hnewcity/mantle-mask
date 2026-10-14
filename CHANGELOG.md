# 更新记录

## 未发布

- 新增「网关设置 → 备份与迁移」：密码加密导出及覆盖恢复全部用户、API Key、上游凭据、设置、历史用量与 Responses 路由。
- 支持跨主密钥迁移；新增完整性校验、覆盖确认、活动请求冲突保护及恢复中断后的启动续做。

## 0.2.0 — 2026-10-12

首次公开发布当前项目快照。

### 网关与控制台

- Amazon Bedrock Mantle 的 OpenAI 兼容 Models、Chat Completions 和 Responses 接口。
- 多上游加权路由、模型别名、故障冷却与 Responses 上游绑定。
- Bedrock API Key / SigV4、多用户与 API Key 隔离。
- SQLite 用量持久化、上游用量报表、日期筛选、按模型图表和 CSV 导出。
- 中文管理控制台、Markdown Playground、移动端布局。

### 开源与交付

- AGPL-3.0-only 许可证、贡献指南、安全政策和第三方声明。
- README 首页截图及可复现的隔离截图流程。
- 测试、Docker 启动验证、GHCR 多架构镜像与标签版 GitHub Release CI。
- Linux amd64 / arm64 二进制与 SHA-256 校验文件。
