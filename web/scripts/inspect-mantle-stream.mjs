// Diagnostic emits framing/terminal metadata only, never headers or generated text.
import { readFile } from "node:fs/promises"
const env = {}
for (const line of (await readFile(process.argv[2], "utf8")).split(/\r?\n/)) {
  const m = line.match(/^\s*(?:export\s+)?(AWS_BEARER_TOKEN_BEDROCK|AWS_REGION)\s*=\s*(.*?)\s*$/)
  if (m) env[m[1]] = m[2].replace(/^(['"])(.*)\1$/, "$2")
}
if (!/^[a-z]{2}(?:-[a-z]+)+-\d$/.test(env.AWS_REGION || "") || !env.AWS_BEARER_TOKEN_BEDROCK) {
  throw new Error("Required Bedrock test variables were not found")
}
const response = await fetch(`https://bedrock-mantle.${env.AWS_REGION}.api.aws/v1/chat/completions`, {
  method: "POST",
  headers: { Authorization: `Bearer ${env.AWS_BEARER_TOKEN_BEDROCK}`, "Content-Type": "application/json" },
  body: JSON.stringify({ model: "openai.gpt-oss-20b", messages: [{ role: "user", content: "Reply only OK." }], stream: true, max_completion_tokens: 128 }),
  signal: AbortSignal.timeout(30000),
})
const text = await response.text()
const frames = []
for (const line of text.split(/\r?\n/)) {
  if (!line.startsWith("data:")) continue
  const data = line.slice(5).trim()
  if (data === "[DONE]") { frames.push({ done: true }); continue }
  try {
    const value = JSON.parse(data)
    frames.push({ object: value.object, type: value.type, choice_count: value.choices?.length, finish_reasons: value.choices?.map(v => v.finish_reason), has_usage: !!value.usage })
  } catch { frames.push({ unparseable: true, length: data.length }) }
}
console.log(JSON.stringify({
  status: response.status, content_type: response.headers.get("content-type"),
  bytes: text.length, lf: (text.match(/\n/g) || []).length, cr: (text.match(/\r/g) || []).length,
  ends_with_blank_line: /\r?\n\r?\n$/.test(text), contains_done: text.includes("[DONE]"), frames,
}, null, 2))
