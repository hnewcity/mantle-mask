import { describe, expect, it } from "vitest"
import { compileModelMappings } from "./model-mappings"

describe("model mapping form validation", () => {
  it("trims the submitted values and preserves multiple model mappings", () => {
    expect(compileModelMappings([
      { id: 3, alias: " chat ", model: " vendor/model-v1 " },
      { id: 7, alias: "reasoning", model: "vendor/model-v2" },
    ]).models).toEqual({ chat: "vendor/model-v1", reasoning: "vendor/model-v2" })
  })

  it("marks every duplicate alias after trimming", () => {
    const result = compileModelMappings([
      { id: 0, alias: "chat", model: "first" },
      { id: 1, alias: " chat ", model: "second" },
    ])
    expect(result.models).toBeNull()
    expect(result.errors.every(error => error.alias?.includes("重复"))).toBe(true)
  })

  it("keeps incomplete rows from silently changing the model allowlist", () => {
    const result = compileModelMappings([
      { id: 0, alias: " ", model: "model" },
      { id: 1, alias: "chat", model: " " },
    ])
    expect(result.models).toBeNull()
    expect(result.errors[0].alias).toBeTruthy()
    expect(result.errors[1].model).toBeTruthy()
  })

  it("does not turn an empty explicit allowlist into passthrough", () => {
    expect(compileModelMappings([]).models).toBeNull()
    expect(compileModelMappings([]).message).toContain("至少")
  })

  it("supports model names that are also JavaScript object properties", () => {
    const result = compileModelMappings([{ id: 0, alias: "__proto__", model: "vendor/model" }])
    expect(Object.keys(result.models!)).toEqual(["__proto__"])
    expect(JSON.parse(JSON.stringify(result.models))["__proto__"]).toBe("vendor/model")
  })
})
