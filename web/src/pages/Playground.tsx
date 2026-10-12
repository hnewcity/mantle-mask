import { useEffect, useRef, useState } from "react"
import { ArrowDown, ArrowUpRight, Bot, CheckCircle2, ChevronDown, Code2, Eraser, Play, RefreshCw, Send, SlidersHorizontal, Square, User } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Select } from "@/components/ui/select"
import { SearchSelect } from "@/components/ui/search-select"
import { Textarea } from "@/components/ui/textarea"
import { Switch } from "@/components/ui/switch"
import { CopyButton, Field, notify, PageTitle } from "@/components/common"
import { MarkdownContent } from "@/components/markdown-content"
import { api, errorMessage, message, session, type Model, type Upstream } from "@/lib/api"
import { consumeSSE } from "@/lib/sse"
import { cn, number } from "@/lib/utils"

type ChatMessage = { role: "user" | "assistant"; content: string }
export function Playground({ upstreams, onComplete }: { upstreams: Upstream[]; onComplete: () => Promise<void> }) {
  const [model, setModel] = useState(""), [models, setModels] = useState<Model[]>([]), [loadingModels, setLoadingModels] = useState(false)
  const [endpoint, setEndpoint] = useState("chat/completions"), [stream, setStream] = useState(true), [system, setSystem] = useState("")
  const [prompt, setPrompt] = useState(""), [messages, setMessages] = useState<ChatMessage[]>([]), [busy, setBusy] = useState(false)
  const [advanced, setAdvanced] = useState("{}"), [showAdvanced, setShowAdvanced] = useState(false), [raw, setRaw] = useState(""), [view, setView] = useState("chat")
  const [error, setError] = useState(""), [meta, setMeta] = useState<{ ms: number; tokens: number; done: boolean } | null>(null)
  const controller = useRef<AbortController | null>(null), bottom = useRef<HTMLDivElement>(null)
  const ready = upstreams.some(u => u.enabled)
  const loadModels = async () => {
    setLoadingModels(true)
    try { const result = await api<{ data: Model[] }>("/playground/models"); setModels(result.data); if (!model && result.data.length) setModel(result.data[0].id) }
    catch (e) { notify(message(e), true) } finally { setLoadingModels(false) }
  }
  useEffect(() => { if (ready) void loadModels(); return () => controller.current?.abort() }, []) // Discover once; the refresh button handles later configuration changes.
  useEffect(() => { bottom.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }) }, [messages])
  async function send() {
    if (!prompt.trim() || !model || busy) return
    let extra: Record<string, unknown>
    try {
      extra = JSON.parse(advanced)
      if (!extra || Array.isArray(extra) || typeof extra !== "object") throw new Error()
    } catch { setError("额外参数必须是 JSON 对象"); return }
    const history: ChatMessage[] = [...messages.filter(m => m.content), { role: "user", content: prompt.trim() }]
    const input = system ? [{ role: "system", content: system }, ...history] : history
    const body = endpoint === "chat/completions" ? { ...extra, model, messages: input, stream } : { store: false, ...extra, model, input, stream }
    setMessages([...history, { role: "assistant", content: "" }]); setPrompt(""); setBusy(true); setError(""); setMeta(null); setRaw("")
    const abort = new AbortController(); controller.current = abort
    const start = performance.now()
    let answer = "", rawText = "", tokens = 0, completed = !stream
    const update = (content: string) => { answer += content; setMessages([...history, { role: "assistant", content: answer }]) }
    const capture = (text: string) => { if (rawText.length < 2 * 1024 * 1024) { rawText += text + "\n"; setRaw(rawText) } }
    const consume = (value: string) => {
      capture(value)
      if (value === "[DONE]") { completed = true; return }
      let data
      try { data = JSON.parse(value) } catch { return }
      if (data.error || data.type === "error") throw new Error(data.error?.message || data.message || "上游返回错误")
      if (data.type === "response.failed") throw new Error(data.response?.error?.message || "响应生成失败")
      if (data.choices?.[0]?.delta?.content) update(data.choices[0].delta.content)
      if (data.type === "response.output_text.delta") update(data.delta || "")
      if (data.type === "response.completed" || data.type === "response.incomplete") completed = true
      const usage = data.usage || data.response?.usage
      if (usage) tokens = usage.total_tokens || (usage.input_tokens || usage.prompt_tokens || 0) + (usage.output_tokens || usage.completion_tokens || 0)
    }
    try {
      const response = await fetch(`/api/admin/playground/${endpoint}`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${session.get()}` }, body: JSON.stringify(body), signal: abort.signal })
      if (!response.ok) {
        if (response.status === 401) window.dispatchEvent(new Event("mantle:unauthorized"))
        throw new Error(await errorMessage(response))
      }
      if (response.headers.get("content-type")?.includes("text/event-stream")) {
        if (!response.body) throw new Error("服务未返回响应流")
        await consumeSSE(response.body, consume)
        if (!completed) throw new Error("响应流在完成前中断")
      } else {
        const data = await response.json(); capture(JSON.stringify(data, null, 2)); completed = true
        if (data.error) throw new Error(data.error.message)
        if (data.choices?.[0]?.message?.content) update(data.choices[0].message.content)
        else if (data.output) for (const item of data.output) for (const part of item.content || []) if (part.text) update(part.text)
        const usage = data.usage
        tokens = usage?.total_tokens || ((usage?.input_tokens || usage?.prompt_tokens || 0) + (usage?.output_tokens || usage?.completion_tokens || 0))
      }
      if (!answer) update("模型未返回文本内容。请切换到「原始响应」查看工具调用或其他输出。")
      setMeta({ ms: Math.round(performance.now() - start), tokens, done: true })
    } catch (e) {
      setError(abort.signal.aborted ? "请求已停止" : message(e)); setMeta({ ms: Math.round(performance.now() - start), tokens, done: false })
    } finally { setBusy(false); controller.current = null; void onComplete() }
  }
  return <><PageTitle eyebrow="MODEL PLAYGROUND" title="在线测试" description="从这里发起真实请求，验证模型、鉴权和流式输出。" action={<span className="playground-tag"><i />通过网关路由</span>} />
    {!ready && <div className="info-strip"><Code2 size={18} /><div><strong>先连接一个上游</strong><span>添加并启用上游后，即可在这里进行真实模型测试。</span></div><Button variant="outline" size="sm" asChild><a href="#/upstreams">配置上游<ArrowUpRight /></a></Button></div>}
    <div className="playground-layout"><aside className="panel playground-config"><div className="panel-heading"><h2><SlidersHorizontal size={16} />请求配置</h2></div><div className="playground-fields"><Field label="API 接口"><Select aria-label="API 接口" disabled={busy} value={endpoint} onValueChange={value => { setEndpoint(value); setMessages([]); setRaw(""); setError(""); setMeta(null) }} options={[{ value: "chat/completions", label: "Chat Completions" }, { value: "responses", label: "Responses" }]} /></Field><Field label="模型" hint="选择发现的模型，也可以直接输入模型名称"><div className="model-input"><SearchSelect aria-label="模型" disabled={busy} placeholder="输入或选择模型 ID" searchPlaceholder="搜索或输入模型 ID…" value={model} onValueChange={setModel} options={models.map(m => ({ value: m.id, label: m.id }))} allowCustom /><Button size="icon" variant="outline" disabled={loadingModels || !ready || busy} aria-label="刷新模型列表" onClick={() => void loadModels()}><RefreshCw className={loadingModels ? "animate-spin" : ""} size={14} /></Button></div></Field><div className="toggle-row"><div><strong>流式输出</strong><p>实时接收 SSE 事件</p></div><Switch disabled={busy} checked={stream} onCheckedChange={setStream} aria-label="流式输出" /></div><Field label="System prompt（可选）"><Textarea rows={4} disabled={busy} value={system} placeholder="为模型设置角色或指令…" onChange={e => setSystem(e.target.value)} /></Field><button type="button" className="advanced-toggle" onClick={() => setShowAdvanced(!showAdvanced)}>额外 JSON 参数<ChevronDown size={14} className={showAdvanced ? "rotate-180" : ""} /></button>{showAdvanced && <Field label="参数对象" hint="例如 temperature、tools、reasoning。具体支持取决于上游模型。"><Textarea className="mono" rows={7} disabled={busy} value={advanced} onChange={e => setAdvanced(e.target.value)} spellCheck={false} /></Field>}<div className="playground-note"><ShieldIcon /><p>测试会调用实际上游并可能产生费用。Responses 默认发送 <code>store: false</code>。</p></div></div></aside>
      <section className="panel playground-output"><div className="playground-output-header"><div className="segmented"><button className={view === "chat" ? "active" : ""} onClick={() => setView("chat")}>模型输出</button><button className={view === "raw" ? "active" : ""} onClick={() => setView("raw")}>原始响应</button></div><Button size="sm" variant="ghost" disabled={busy || !messages.length} onClick={() => { setMessages([]); setRaw(""); setMeta(null); setError("") }}><Eraser size={14} />清空</Button></div><div className="conversation">
        {view === "raw" ? <div className="raw-output">{raw ? <><CopyButton value={raw} label="复制响应" /><pre>{raw}</pre></> : <p className="muted">发起请求后，这里将显示 JSON 或 SSE 原始事件。</p>}</div> : messages.length ? messages.map((m, i) => <div className={`chat-message ${m.role}`} key={i}><div className="chat-avatar">{m.role === "user" ? <User size={16} /> : <Bot size={18} />}</div><div><strong>{m.role === "user" ? "你" : "模型"}{m.role === "assistant" && <span>{model}</span>}</strong><div className="chat-content">{m.content ? (m.role === "assistant" ? <MarkdownContent content={m.content} /> : m.content) : busy ? <span className="typing-dots"><i /><i /><i /></span> : "未收到文本输出"}</div></div></div>) : <div className="playground-empty"><div className="playground-orb"><Code2 size={31} /></div><h2>让第一个请求发生。</h2><p>选择模型，输入提示词。<br />从第一段回复，确认整个链路。</p><div className="prompt-suggestions">{["用三句话介绍你自己", "写一个 Go HTTP 服务示例", "解释什么是流式响应"].map(text => <button key={text} disabled={!ready} onClick={() => setPrompt(text)}>{text}<ArrowUpRight size={13} /></button>)}</div><span><ArrowDown size={13} />从下方开始</span></div>}
        <div ref={bottom} /></div>{error && <p className="playground-error" role="alert">{error}</p>}{meta && <div className={cn("response-meta", !meta.done && "incomplete")}><span>{meta.done ? <CheckCircle2 size={13} /> : <Square size={12} />}{meta.done ? "响应完成" : "请求结束"}</span><span>{number(meta.ms)} ms</span><span>{number(meta.tokens)} tokens</span></div>}
        <form className="composer" onSubmit={e => { e.preventDefault(); void send() }}><Textarea aria-label="输入提示词" placeholder={ready ? "输入你的提示词…" : "请先配置一个上游端点"} value={prompt} onChange={e => setPrompt(e.target.value)} disabled={!ready || busy} onKeyDown={e => { if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); void send() } }} /><div><span>Ctrl / ⌘ + Enter 发送</span>{busy ? <Button type="button" variant="outline" onClick={() => controller.current?.abort()}><Square size={13} />停止生成</Button> : <Button disabled={!ready || !model || !prompt.trim()}><Send size={14} />发送请求</Button>}</div></form>
      </section></div>
  </>
}
function ShieldIcon() { return <Play size={15} /> }
