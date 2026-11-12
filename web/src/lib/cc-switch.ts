export type CCSwitchApp = "codex" | "claude"

type Provider = { app: CCSwitchApp; name: string; endpoint: string; apiKey: string; model: string }

// CC Switch provider deep-link protocol:
// https://github.com/farion1231/cc-switch/blob/main/docs/guides/deeplink-site-integration-guide-en.md
export function buildCCSwitchLink({ app, name, endpoint, apiKey, model }: Provider): string {
  if (!name.trim() || !apiKey.trim() || !model.trim()) throw new Error("请填写供应商名称、密钥和模型")
  const params = new URLSearchParams({
    resource: "provider",
    app,
    name: name.trim(),
    endpoint,
    apiKey: apiKey.trim(),
    model: model.trim(),
    apiFormat: app === "codex" ? "openai_responses" : "openai_chat",
  })
  if (app === "claude") {
    // Route every Claude model tier to the selected gateway model.
    for (const tier of ["haikuModel", "sonnetModel", "opusModel"]) params.set(tier, model.trim())
  }
  return `ccswitch://v1/import?${params}`
}
