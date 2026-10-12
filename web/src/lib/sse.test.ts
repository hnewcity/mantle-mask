import { describe, expect, it } from "vitest"
import { consumeSSE } from "./sse"

function chunks(parts: Uint8Array[]) {
  return new ReadableStream<Uint8Array>({ start(controller) { parts.forEach(part => controller.enqueue(part)); controller.close() } })
}
describe("SSE transport", () => {
  it("handles every possible byte split including UTF-8 and CRLF", async () => {
    const source = new TextEncoder().encode(': ping\r\n\r\nevent: delta\r\ndata: {"text":"你好🌱"}\r\n\r\ndata: [DONE]\n\n')
    for (let i = 1; i < source.length; i++) {
      const events: string[] = []
      await consumeSSE(chunks([source.slice(0, i), source.slice(i)]), value => events.push(value))
      expect(events).toEqual(['{"text":"你好🌱"}', "[DONE]"])
    }
  })
  it("joins multi-line data and ignores comments and event names", async () => {
    const events: string[] = []
    await consumeSSE(chunks([new TextEncoder().encode(': keepalive\n\nevent: test\ndata: first\ndata: second\n\n')]), value => events.push(value))
    expect(events).toEqual(["first\nsecond"])
  })
  it("propagates upstream error events and releases the reader", async () => {
    const stream = chunks([new TextEncoder().encode('data: {"error":{"message":"failed"}}\n\n')])
    await expect(consumeSSE(stream, () => { throw new Error("failed") })).rejects.toThrow("failed")
    expect(stream.locked).toBe(false)
  })
})
