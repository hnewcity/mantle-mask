import { expect, test } from "@playwright/test"
import OpenAI from "openai"

test("unmodified OpenAI SDK supports models, Chat, Responses, and response retrieval", async ({ request }) => {
  const headers = { Authorization: "Bearer e2e-admin-token-only-for-local-tests" }
  const setup = await request.post("/api/admin/upstreams", {
    headers,
    data: { name: "SDK fixture", base_url: "http://127.0.0.1:18090/v1", region: "us-east-1", auth_mode: "api_key", api_key: "mock-bedrock-key", weight: 1, enabled: true, models: {} },
  })
  expect(setup.ok()).toBe(true)
  const keyResult = await request.post("/api/admin/keys", { headers, data: { name: "sdk-conformance" } })
  const { token, key } = await keyResult.json()
  const client = new OpenAI({ apiKey: token, baseURL: "http://127.0.0.1:18080/v1", maxRetries: 0 })
  const models = await client.models.list()
  expect(models.data.some(m => m.id === "mock-model")).toBe(true)
  const completion = await client.chat.completions.create({ model: "mock-model", messages: [{ role: "user", content: "Hi" }] })
  expect(completion.choices[0].message.content).toContain("Mantle Mask")
  const chatStream = await client.chat.completions.create({ model: "mock-model", messages: [{ role: "user", content: "Hi" }], stream: true })
  let text = ""
  for await (const chunk of chatStream) text += chunk.choices[0]?.delta.content || ""
  expect(text).toContain("Mantle Mask")
  const response = await client.responses.create({ model: "mock-model", input: "Hi" })
  expect(response.id).toBe("resp_mock")
  const retrieved = await client.responses.retrieve(response.id)
  expect(retrieved.id).toBe(response.id)
  const continued = await client.responses.create({ previous_response_id: response.id, input: "Again" })
  expect(continued.id).toBe("resp_mock")
  const stream = await client.responses.create({ model: "mock-model", input: "Hi", stream: true })
  let output = ""
  for await (const event of stream) if (event.type === "response.output_text.delta") output += event.delta
  expect(output).toContain("Mantle Mask")
  await request.delete(`/api/admin/keys/${key.id}`, { headers })
})
