// Opt-in AWS integration test. Reads only the two named Bedrock variables.
// Credentials are held in memory and an encrypted temporary data directory.
import { readFile, mkdtemp, mkdir, rm, writeFile } from "node:fs/promises"
import { randomBytes } from "node:crypto"
import { tmpdir } from "node:os"
import { createServer } from "node:net"
import { spawn } from "node:child_process"
import { fileURLToPath } from "node:url"
import path from "node:path"
import OpenAI from "openai"

const root = fileURLToPath(new URL("../../", import.meta.url))
const envPath = process.argv[2]
if (!envPath) throw new Error("Usage: node web/scripts/aws-smoke.mjs /path/to/credentials.env [--discover-only]")
const env = {}
for (const line of (await readFile(envPath, "utf8")).split(/\r?\n/)) {
  const match = line.match(/^\s*(?:export\s+)?(AWS_BEARER_TOKEN_BEDROCK|AWS_REGION)\s*=\s*(.*?)\s*$/)
  if (match) env[match[1]] = match[2].replace(/^(['"])(.*)\1$/, "$2")
}
const region = env.AWS_REGION
if (!/^[a-z]{2}(?:-[a-z]+)+-\d$/.test(region || "") || !env.AWS_BEARER_TOKEN_BEDROCK) {
  throw new Error("AWS_REGION or AWS_BEARER_TOKEN_BEDROCK is missing/invalid")
}
const report = { timestamp: new Date().toISOString(), region, checks: [], models: [] }
const dir = await mkdtemp(path.join(tmpdir(), "mantle-mask-aws-"))
const listener = createServer()
await new Promise(resolve => listener.listen(0, "127.0.0.1", resolve))
const port = listener.address().port
await new Promise(resolve => listener.close(resolve))
const base = `http://127.0.0.1:${port}`
const adminToken = randomBytes(32).toString("hex")
const child = spawn(path.join(root, "bin", "mantle-mask"), [], {
  cwd: root, stdio: "ignore",
  env: { ...process.env, MANTLE_ADDR: `127.0.0.1:${port}`, MANTLE_DATA_DIR: dir, MANTLE_ADMIN_TOKEN: adminToken, MANTLE_MASTER_KEY: "" },
})
const exited = new Promise(resolve => child.once("exit", resolve))
child.on("error", () => { report.checks.push({ name: "startup", passed: false, code: "process_start_failed" }) })

function sanitize(text) {
  return String(text || "").replaceAll(env.AWS_BEARER_TOKEN_BEDROCK, "[redacted]")
    .replace(/arn:aws[a-z-]*:[^\s"]+/g, "[aws-resource]")
    .slice(0, 350)
}
async function admin(route, method = "GET", body) {
  const response = await fetch(base + "/api/admin" + route, {
    method, headers: { Authorization: `Bearer ${adminToken}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000),
  })
  const data = await response.json()
  if (!response.ok) throw Object.assign(new Error(sanitize(data.error?.message)), { status: response.status, code: data.error?.code })
  return data
}
async function check(name, fn) {
  const start = performance.now()
  try {
    const detail = await fn()
    const result = { name, passed: true, duration_ms: Math.round(performance.now() - start), ...detail }
    report.checks.push(result)
    console.log(JSON.stringify(result))
    return detail
  } catch (error) {
    const result = { name, passed: false, duration_ms: Math.round(performance.now() - start), status: error.status, code: error.code, message: sanitize(error.message) }
    report.checks.push(result)
    console.log(JSON.stringify(result))
    return null
  }
}
try {
  let ready = false
  for (let i = 0; i < 120; i++) {
    if (child.exitCode !== null) break
    try { ready = (await fetch(base + "/healthz")).ok } catch {}
    if (ready) break
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  if (!ready) throw new Error("Temporary gateway did not start")
  const baseURL = `https://bedrock-mantle.${region}.api.aws/v1`
  const config = { name: "AWS integration test (temporary)", base_url: baseURL, region, auth_mode: "api_key", api_key: env.AWS_BEARER_TOKEN_BEDROCK, weight: 1, enabled: true, models: {} }
  const { id } = await admin("/upstreams", "POST", config)
  const discovery = await check("mantle-model-discovery", async () => {
    const data = await admin(`/upstreams/${id}/test`, "POST")
    report.models = data.models.map(m => m.id)
    return { count: data.models.length }
  })
  if (!discovery) throw new Error("Model discovery failed")
  console.log(JSON.stringify({ region, available_models: report.models.slice(0, 20), total: report.models.length }))
  if (!process.argv.includes("--discover-only")) {
    const preferred = ["openai.gpt-oss-20b", "openai.gpt-oss-120b"]
    const model = preferred.find(id => report.models.includes(id)) || report.models.find(id => /gpt-oss/.test(id))
    if (!model) throw new Error("No GPT OSS model found; choose a model explicitly before paid inference")
    report.model = model
    const user = await admin("/users", "POST", { name: "AWS smoke user (temporary)" })
    const { token } = await admin("/keys", "POST", { name: "aws-smoke-temporary", user_id: user.id })
    const expected = { input: 0, output: 0, known: 0 }
    function observe(usage) {
      const input = usage?.input_tokens ?? usage?.prompt_tokens
      const output = usage?.output_tokens ?? usage?.completion_tokens
      if (typeof input === "number" && typeof output === "number") {
        expected.input += input; expected.output += output; expected.known++
      }
    }
    const client = new OpenAI({ apiKey: token, baseURL: base + "/v1", maxRetries: 0, timeout: 45000 })
    const input = "Reply with only the word OK."
    await check("openai-sdk-models", async () => ({ count: (await client.models.list()).data.length }))
    await check("chat-nonstream", async () => {
      const response = await client.chat.completions.create({ model, messages: [{ role: "user", content: input }], max_completion_tokens: 256 })
      if (!response.id || !response.choices?.length) throw new Error("Invalid Chat Completions response")
      observe(response.usage)
      return { returned_model: response.model, text_received: !!response.choices[0].message.content, finish_reason: response.choices[0].finish_reason, usage: response.usage }
    })
    await check("chat-stream", async () => {
      const stream = await client.chat.completions.create({ model, messages: [{ role: "user", content: input }], max_completion_tokens: 256, stream: true })
      let chunks = 0, text = "", usage
      for await (const chunk of stream) { chunks++; text += chunk.choices[0]?.delta?.content || ""; if (chunk.usage) usage = chunk.usage }
      observe(usage)
      if (!chunks) throw new Error("No stream chunks")
      return { chunks, text_received: !!text, usage_reported: !!usage }
    })
    await check("responses-stream", async () => {
      const stream = await client.responses.create({ model, input, max_output_tokens: 256, stream: true, store: false })
      let events = 0, text = "", terminal = "", usage
      for await (const event of stream) {
        events++
        if (event.type === "response.output_text.delta") text += event.delta
        if (["response.completed", "response.incomplete", "response.failed"].includes(event.type)) terminal = event.type
        if (event.response?.usage) usage = event.response.usage
        if (event.type === "error") throw new Error(event.message)
      }
      if (!terminal || terminal === "response.failed") throw new Error("Response stream did not complete")
      observe(usage)
      return { events, terminal, text_received: !!text, usage_reported: !!usage }
    })
    const created = []
    await check("responses-multiturn-and-retrieve", async () => {
      try {
        const first = await client.responses.create({ model, input, max_output_tokens: 256, store: true })
        created.push(first.id)
        observe(first.usage)
        const retrieved = await client.responses.retrieve(first.id)
        if (retrieved.id !== first.id) throw new Error("Wrong response retrieved")
        const second = await client.responses.create({ previous_response_id: first.id, input: "Reply OK again.", max_output_tokens: 256 })
        created.push(second.id)
        observe(second.usage)
        return { first_status: first.status, second_status: second.status, retrieved: true, model_inherited: true, usage: second.usage }
      } finally {
        for (const responseId of created) {
          await check("cleanup-stored-response", async () => {
            const response = await fetch(`${base}/v1/responses/${responseId}`, { method: "DELETE", headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(30000) })
            if (!response.ok) throw Object.assign(new Error("Delete returned a non-success status"), { status: response.status })
            return { deleted: true }
          })
        }
      }
    })
    await check("per-user-token-accounting", async () => {
      const report = await admin(`/usage?all=true&user_id=${user.id}`)
      const total = report.totals
      if (total.requests !== 5 || total.input_tokens !== expected.input || total.output_tokens !== expected.output || total.known_usage_requests !== expected.known) {
        throw new Error("Persisted per-user totals differ from observed SDK usage")
      }
      const mineResponse = await fetch(base + "/api/me/usage?all=true&user_id=another-user", { headers: { Authorization: `Bearer ${token}` } })
      const mine = await mineResponse.json()
      if (!mineResponse.ok || mine.totals.total_tokens !== total.total_tokens || mine.by_user[0]?.id !== user.id) throw new Error("User report scope is incorrect")
      return { requests: total.requests, input_tokens: total.input_tokens, output_tokens: total.output_tokens, known_usage_requests: total.known_usage_requests, missing_usage_requests: total.missing_usage_requests, scoped: true }
    })
    const metrics = await admin("/overview")
    report.metrics = { requests: metrics.requests, errors: metrics.errors, tokens: metrics.tokens }
  }
} catch (error) {
  report.fatal = sanitize(error.message)
  console.log(JSON.stringify({ fatal: report.fatal }))
  process.exitCode = 1
} finally {
  child.kill("SIGTERM")
  await Promise.race([exited, new Promise(resolve => { const timer = setTimeout(resolve, 16000); timer.unref() })])
  if (child.exitCode === null) { child.kill("SIGKILL"); await exited }
  await rm(dir, { recursive: true, force: true })
  await mkdir(path.join(root, "test-results"), { recursive: true })
  await writeFile(path.join(root, "test-results", "aws-smoke.json"), JSON.stringify(report, null, 2))
  if (report.checks.some(c => !c.passed)) process.exitCode = 1
}
