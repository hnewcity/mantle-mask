import { describe, expect, it } from "vitest"
import { buildCCSwitchLink } from "./cc-switch"

const provider = {
  app: "codex" as const, name: "Mantle Mask · 开发 + QA & 测试",
  endpoint: "https://gateway.example:8443/v1", apiKey: "sk-mm-test+/=&?#", model: "team/coder:latest + 中文",
}

describe("CC Switch provider protocol", () => {
  it("round-trips credentials, Unicode and reserved characters as single parameters", () => {
    const url = new URL(buildCCSwitchLink(provider))
    expect(`${url.protocol}//${url.host}${url.pathname}`).toBe("ccswitch://v1/import")
    expect(Object.fromEntries(url.searchParams)).toEqual({
      resource: "provider", ...provider, apiFormat: "openai_responses",
    })
    expect(url.hash).toBe("")
  })

  it("uses Chat Completions for Claude and maps every model tier", () => {
    const params = new URL(buildCCSwitchLink({ ...provider, app: "claude" })).searchParams
    expect(params.get("app")).toBe("claude")
    expect(params.get("apiFormat")).toBe("openai_chat")
    for (const tier of ["model", "haikuModel", "sonnetModel", "opusModel"]) expect(params.get(tier)).toBe(provider.model)
    expect(params.has("enabled")).toBe(false)
  })

  it.each(["name", "apiKey", "model"] as const)("rejects a missing %s instead of importing unusable defaults", field => {
    expect(() => buildCCSwitchLink({ ...provider, [field]: "   " })).toThrow()
  })
})
