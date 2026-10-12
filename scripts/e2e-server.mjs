// Isolated test fixture. Never use these credentials or ports for deployment.
import { createServer } from "node:http"
import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import { spawn } from "node:child_process"

const dir = await mkdtemp(path.join(tmpdir(), "mantle-mask-e2e-"))
const markdownAnswer = [
  "# Markdown 回复",
  "正文包含 **加粗**、*斜体*、~~删除线~~ 和 `inline code`。",
  "- 第一项\n  - 嵌套项\n- 第二项",
  "1. 第一步\n2. 第二步",
  "> 引用内容",
  "[文档](https://example.com/docs)",
  "- [x] 已完成\n- [ ] 待处理",
  '```go\nfmt.Println("<hello>")\nconst endpoint = "https://example.com/' + "long-path/".repeat(30) + '"\n```',
  "| 模型 | 状态 | 输入 Token | 输出 Token |\n| --- | --- | ---: | ---: |\n| mock-model-with-a-long-name | **可用** | 100 | 200 |",
  "---",
  "Markdown 渲染完成。",
].join("\n\n")
const mock = createServer(async (req, res) => {
  res.setHeader("Content-Type", "application/json")
  if (req.headers.authorization !== "Bearer mock-bedrock-key") {
    res.writeHead(401); res.end(JSON.stringify({ error: { message: "Invalid fixture key" } })); return
  }
  if (req.url === "/v1/models") {
    res.end(JSON.stringify({ object: "list", data: [{ id: "mock-model", object: "model", created: 0, owned_by: "e2e-fixture" }] })); return
  }
  if (req.url.startsWith("/v1/responses/resp_mock")) {
    res.end(JSON.stringify({ id: "resp_mock", object: "response", status: "completed", output: [] })); return
  }
  const chunks = []
  for await (const chunk of req) chunks.push(chunk)
  const input = JSON.parse(Buffer.concat(chunks).toString() || "{}")
  const messages = input.messages || input.input
  const markdown = Array.isArray(messages) && messages.at(-1)?.content?.includes("[markdown-fixture]")
  const answer = markdown ? markdownAnswer : "你好！Mantle Mask 的流式代理运行正常。"
  const responses = req.url === "/v1/responses"
  const usage = responses ? { input_tokens: 8, output_tokens: 12, total_tokens: 20 } : { prompt_tokens: 8, completion_tokens: 12, total_tokens: 20 }
  if (!input.stream) {
    res.end(JSON.stringify(responses ? {
      id: "resp_mock", object: "response", model: input.model, usage,
      output: [{ type: "message", role: "assistant", content: [{ type: "output_text", text: answer }] }],
    } : { id: "chatcmpl_mock", object: "chat.completion", model: input.model, usage, choices: [{ index: 0, message: { role: "assistant", content: answer }, finish_reason: "stop" }] }))
    return
  }
  res.setHeader("Content-Type", "text/event-stream")
  res.flushHeaders()
  if (responses) res.write(`event: response.created\ndata: ${JSON.stringify({ type: "response.created", response: { id: "resp_mock", object: "response" } })}\n\n`)
  const deltas = markdown ? ["# Markdown 回复\n\n", ...markdownAnswer.slice("# Markdown 回复\n\n".length).match(/[\s\S]{1,40}/g)] : ["你好！", "Mantle Mask ", "的流式代理", "运行正常。"]
  for (const text of deltas) {
    await new Promise(resolve => setTimeout(resolve, markdown ? 100 : 45))
    const event = responses ? { type: "response.output_text.delta", delta: text } : { id: "chatcmpl_mock", object: "chat.completion.chunk", choices: [{ index: 0, delta: { content: text } }] }
    res.write(`data: ${JSON.stringify(event)}\n\n`)
  }
  if (responses) res.end(`event: response.completed\ndata: ${JSON.stringify({ type: "response.completed", response: { id: "resp_mock", usage } })}\n\n`)
  else res.end(`data: ${JSON.stringify({ choices: [], usage })}\n\ndata: [DONE]\n\n`)
})
await new Promise(resolve => mock.listen(18090, "127.0.0.1", resolve))
const process = spawn(path.resolve("..", "bin", "mantle-mask"), [], {
  cwd: path.resolve(".."),
  env: {
    ...globalThis.process.env,
    MANTLE_ADDR: "127.0.0.1:18080",
    MANTLE_DATA_DIR: dir,
    MANTLE_ADMIN_TOKEN: "e2e-admin-token-only-for-local-tests",
  },
  stdio: "inherit",
})
let closing = false
async function close() {
  if (closing) return
  closing = true
  process.kill("SIGTERM")
  mock.close()
  await new Promise(resolve => process.once("exit", resolve))
  await rm(dir, { recursive: true, force: true })
  globalThis.process.exit(0)
}
globalThis.process.on("SIGTERM", close)
globalThis.process.on("SIGINT", close)
process.on("exit", code => { if (!closing) globalThis.process.exit(code || 0) })
