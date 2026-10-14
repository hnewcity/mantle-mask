# 参考价格数据源

这些数据在管理员点击同步时由服务端拉取，不需要安装 LiteLLM，也不携带上游凭据。管理员手动覆盖优先于任何参考数据。

## AWS Price List

- 官方公开数据，不是开源价格库。
- 数据模板：`https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonBedrock/current/{region}/index.json`
- 官方说明：`https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-the-aws-price-list-bulk-api.html`
- 仅解析区域一致、使用类型包含 Mantle、Standard 档位、OnDemand 的完整输入 / 输出价格。货币必须是 USD，单位严格识别 Token、1K tokens 或 1M tokens；不把其他请求费或时间费当作 Token 费。
- 单 Token 价乘以 1,000,000，千 Token 价乘以 1,000；百万 Token 价直接使用。分段 / 不明确的 AWS 价格不强行合成平价。

## LiteLLM

- 仓库：`https://github.com/BerriAI/litellm`
- 价格文件：`https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`
- 价格表位于仓库非 enterprise 部分；许可见上游根目录 LICENSE。
- 只读取 `bedrock_mantle/` 条目，不用模型原厂 / OpenRouter 价格冒充 Bedrock 价格。优先使用准确区域条目；GovCloud 不回退到普通商业区域价格。
- 输入、输出、缓存读取字段为 USD / Token，乘以 1,000,000。保留可识别的 `*_above_{N}k_tokens` 长上下文阶梯，不将缓存创建 / TTL 价格误作缓存读取价。

## models.dev

- 仓库：`https://github.com/anomalyco/models.dev`（MIT，以上游当前 LICENSE 为准）。
- 数据文件：`https://models.dev/api.json`
- 仅使用 `amazon-bedrock` 提供商的 `cost`，单位已经是 USD / 百万 Token。
- 读取 `input`、`output`、可选 `cache_read` 以及可识别的 `context_over_{N}k`。不混用其他提供商价格，也不把缺失字段解释为免费。

## 安全与准确性边界

- 数据源 URL 固定，区域字符串严格校验；只允许有限的 HTTPS 白名单重定向。每个源限制 32 MiB、整次下载限制 128 MiB，HTTP 请求限制 20 秒，整次同步限制 90 秒、4 个并发请求、32 个区域。
- 缺少必要价格、非有限值、负价、未知单位和不完整阶梯都不会生成免费 / 基础价格。社区数据或缓存可能滞后，使用前应核对上游计费说明。
- 不承诺任何一个目录覆盖全部新模型，也不承诺固定的模型条目数量。当前参考价不是历史账单，Standard 之外档位和非 Token 费用不在此估算中。
- 本仓库不捆绑上述完整价格文件。分发下载的价格数据时，请保留上游来源及适用许可信息。
