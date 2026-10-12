/** Incremental SSE parser: handles UTF-8 split across chunks, CRLF, and multi-line data. */
export async function consumeSSE(
  stream: ReadableStream<Uint8Array>,
  onData: (data: string) => void,
): Promise<void> {
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  let pending = ""
  let lines: string[] = []
  const process = (text: string, final = false) => {
    pending += text
    if (pending.length > 2 * 1024 * 1024) throw new Error("流式事件过大")
    let index: number
    while ((index = pending.indexOf("\n")) !== -1) {
      const line = pending.slice(0, index).replace(/\r$/, "")
      pending = pending.slice(index + 1)
      if (!line) {
        if (lines.length) onData(lines.join("\n"))
        lines = []
      } else if (line.startsWith("data:")) {
        lines.push(line.slice(5).replace(/^ /, ""))
      }
    }
    if (final && lines.length) onData(lines.join("\n"))
  }
  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) { process(decoder.decode(), true); break }
      process(decoder.decode(value, { stream: true }))
    }
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
