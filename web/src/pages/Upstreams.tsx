import { useRef, useState } from "react"
import { ArrowUpRight, CheckCircle2, ChevronDown, Edit3, Globe2, Loader2, Plus, Radio, Server, ShieldCheck, Trash2, Workflow } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
import { ModelMappingEditor } from "@/components/model-mapping-editor"
import { Switch } from "@/components/ui/switch"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Confirm, Empty, Field, HealthBadge, notify, PageTitle } from "@/components/common"
import { api, message, type Health, type Model, type Upstream } from "@/lib/api"
import { compileModelMappings, type ModelMapping } from "@/lib/model-mappings"

export function upstreamPayload(upstream: Upstream) {
  const { has_credentials: _hasCredentials, ...payload } = upstream
  return payload
}

function UpstreamForm({ upstream, onClose, onSaved }: { upstream?: Upstream; onClose: () => void; onSaved: () => Promise<void> }) {
  const [form, setForm] = useState({
    name: upstream?.name || "", base_url: upstream?.base_url || "https://bedrock-mantle.us-east-1.api.aws/v1",
    region: upstream?.region || "us-east-1", auth_mode: upstream?.auth_mode || "api_key", signing_service: upstream?.signing_service || "bedrock-mantle",
    weight: upstream?.weight || 1, project: upstream?.project || "", profile: upstream?.profile || "",
    enabled: upstream?.enabled ?? true, api_key: "", access_key_id: "", secret_access_key: "", session_token: "", clear_credentials: false,
  })
  const [mappings, setMappings] = useState<ModelMapping[]>(() => Object.entries(upstream?.models || {}).map(([alias, model], id) => ({ id, alias, model })))
  const [mappingMode, setMappingMode] = useState<"all" | "mapped">(mappings.length ? "mapped" : "all")
  const nextMappingID = useRef(mappings.length)
  const formRef = useRef<HTMLFormElement>(null)
  const [showMappingErrors, setShowMappingErrors] = useState(false)
  const compiledMappings = compileModelMappings(mappings)
  const [busy, setBusy] = useState(false), [error, setError] = useState("")
  function field(key: keyof typeof form, value: string | number | boolean) { setForm(prev => ({ ...prev, [key]: value })) }
  function addMapping(focus = true) {
    const id = nextMappingID.current++
    setMappings(rows => [...rows, { id, alias: "", model: "" }])
    if (focus) requestAnimationFrame(() => {
      const input = formRef.current?.elements.namedItem(`mapping-alias-${id}`)
      if (input instanceof HTMLInputElement) input.focus()
    })
  }
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}><DialogContent className="upstream-dialog">
    <DialogHeader className="upstream-dialog-header"><DialogTitle>{upstream ? "编辑上游端点" : "连接一个新的上游"}</DialogTitle><DialogDescription>连接端点，配置凭据，选择向应用开放的模型。</DialogDescription></DialogHeader>
    <form ref={formRef} className="upstream-form" onInvalid={event => {
      const details = (event.target as HTMLElement).closest("details")
      if (details) details.open = true
    }} onSubmit={async event => {
      event.preventDefault(); setError("")
      if (mappingMode === "mapped" && !compiledMappings.models) {
        setShowMappingErrors(true)
        const index = compiledMappings.errors.findIndex(error => error.alias || error.model)
        if (index >= 0) {
          const field = compiledMappings.errors[index].alias ? "alias" : "model"
          const input = formRef.current?.elements.namedItem(`mapping-${field}-${mappings[index].id}`)
          requestAnimationFrame(() => { if (input instanceof HTMLInputElement) input.focus() })
        }
        return
      }
      setBusy(true)
      try {
        const models = mappingMode === "all" ? {} : compiledMappings.models
        await api(upstream ? `/upstreams/${upstream.id}` : "/upstreams", { method: upstream ? "PUT" : "POST", body: JSON.stringify({ ...form, models }) })
        await onSaved(); notify(upstream ? "上游配置已更新" : "上游已添加，可以进行连接检测"); onClose()
      } catch (e) { setError(message(e)) } finally { setBusy(false) }
    }}>
      <div className="dialog-scroll"><div className="form-grid">
        <Field label="端点名称"><Input value={form.name} placeholder="例如：生产环境 · 美国东部" required maxLength={100} onChange={e => field("name", e.target.value)} /></Field>
        <Field label="AWS 区域"><Input value={form.region} placeholder="us-east-1" onChange={e => field("region", e.target.value)} required={form.auth_mode === "sigv4"} /></Field>
        <Field label="Base URL" className="col-span-2" hint="填写模型推理地址（/v1 或 /openai/v1）；发现模型时自动使用 /v1/models。"><Input className="mono" type="url" value={form.base_url} required onChange={e => field("base_url", e.target.value)} /></Field>
      </div><div className="form-section-label"><ShieldCheck size={15} />鉴权方式</div>
      <div className="auth-options"><button type="button" aria-pressed={form.auth_mode === "api_key"} className={form.auth_mode === "api_key" ? "selected" : ""} onClick={() => field("auth_mode", "api_key")}><span className="radio-indicator" /><div><strong>Bedrock API Key</strong><small>使用 Amazon Bedrock 的 Bearer 令牌</small></div></button><button type="button" aria-pressed={form.auth_mode === "sigv4"} className={form.auth_mode === "sigv4" ? "selected" : ""} onClick={() => field("auth_mode", "sigv4")}><span className="radio-indicator" /><div><strong>AWS SigV4</strong><small>使用访问密钥或服务端 IAM 角色</small></div></button></div>
      {form.auth_mode === "api_key" ? <Field label="Bedrock API Key" hint={upstream?.has_credentials ? "留空保留已保存的凭据。凭据仅在后端加密存储。" : "填写 Bedrock API Key，不是 OpenAI API Key。"}><Input type="password" autoComplete="new-password" value={form.api_key} required={!upstream?.has_credentials || upstream.auth_mode !== "api_key"} placeholder={upstream?.has_credentials ? "已保存 · 留空保留" : "输入 Bedrock API Key"} onChange={e => field("api_key", e.target.value)} /></Field> : <div className="form-grid">
        <Field label="Access Key ID" hint="留空可使用服务端 AWS 凭据链"><Input autoComplete="off" value={form.access_key_id} onChange={e => field("access_key_id", e.target.value)} /></Field><Field label="Secret Access Key"><Input type="password" autoComplete="new-password" value={form.secret_access_key} onChange={e => field("secret_access_key", e.target.value)} /></Field>
        <Field label="Session Token（临时凭据）" className="col-span-2"><Input type="password" autoComplete="new-password" value={form.session_token} onChange={e => field("session_token", e.target.value)} /></Field>
        <Field label="AWS Profile（可选）"><Input value={form.profile} placeholder="default" onChange={e => field("profile", e.target.value)} /></Field>
        <Field label="签名服务"><Select aria-label="签名服务" value={form.signing_service} onValueChange={value => field("signing_service", value)} options={[{ value: "bedrock-mantle", label: "bedrock-mantle" }, { value: "bedrock", label: "bedrock" }]} /></Field>
        {upstream?.has_credentials && <label className="checkbox-label col-span-2"><input type="checkbox" checked={form.clear_credentials} onChange={e => field("clear_credentials", e.target.checked)} />清除已保存的密钥，改用服务端凭据链</label>}
      </div>}
      <ModelMappingEditor mode={mappingMode} rows={mappings} errors={showMappingErrors ? compiledMappings.errors : []}
        onModeChange={mode => { setMappingMode(mode); setShowMappingErrors(false); if (mode === "mapped" && !mappings.length) addMapping(false) }}
        onChange={(id, field, value) => setMappings(rows => rows.map(row => row.id === id ? { ...row, [field]: value } : row))}
        onAdd={() => addMapping()}
        onRemove={id => setMappings(rows => rows.filter(row => row.id !== id))}
      />
      <details className="upstream-advanced">
        <summary><Workflow size={15} /><span>路由与项目</span><small>可选设置</small><ChevronDown size={15} /></summary>
        <div className="form-grid">
        <Field label="路由权重" hint="相同模型按权重分配请求"><Input type="number" min={1} max={100} value={form.weight} onChange={e => field("weight", Number(e.target.value))} required /></Field>
        <Field label="Project（可选）" hint="填写后覆盖客户端的 OpenAI-Project"><Input value={form.project} placeholder="default / proj_…" onChange={e => field("project", e.target.value)} /></Field>
        </div>
      </details></div>
      <DialogFooter className="upstream-dialog-footer">
        {(error || (mappingMode === "mapped" && showMappingErrors && compiledMappings.message)) && <p className="upstream-save-error" role="alert">{error || compiledMappings.message}</p>}
        <div className="upstream-footer-controls">
          <label className="upstream-enable"><Switch aria-label="启用这个上游" checked={form.enabled} disabled={busy} onCheckedChange={v => field("enabled", v)} /><span>启用上游</span></label>
          <div className="upstream-footer-actions"><Button type="button" variant="outline" disabled={busy} onClick={onClose}>取消</Button><Button disabled={busy}>{busy && <Loader2 className="animate-spin" />}保存上游</Button></div>
        </div>
      </DialogFooter>
    </form>
  </DialogContent></Dialog>
}

export function Upstreams({ upstreams, health, reload }: { upstreams: Upstream[]; health: Record<string, Health>; reload: () => Promise<void> }) {
  const [editing, setEditing] = useState<Upstream | "new" | null>(null)
  const [testing, setTesting] = useState<string | null>(null), [search, setSearch] = useState("")
  const [models, setModels] = useState<{ name: string; data: Model[] } | null>(null)
  const [toggling, setToggling] = useState<string | null>(null)
  const filtered = upstreams.filter(u => `${u.name} ${u.region} ${u.base_url}`.toLowerCase().includes(search.toLowerCase()))
  return <><PageTitle eyebrow="UPSTREAM CONNECTIONS" title="上游端点" description="连接一个或多个 Bedrock Mantle 端点，按模型和权重分配请求。" action={<Button onClick={() => setEditing("new")}><Plus />添加上游</Button>} />
    <div className="info-strip"><Workflow size={18} /><div><strong>加权轮询 + 自动故障切换</strong><span>对 429 / 502 / 503 / 504 及连接失败尝试其他上游；流开始后和已绑定的 Responses 请求不切换。</span></div><span className="tag">SMART ROUTING</span></div>
    <div className="list-toolbar"><div><strong>全部端点</strong><span className="count-label">{upstreams.length}</span></div><Input aria-label="搜索端点" placeholder="搜索名称、区域或地址…" className="search-input" value={search} onChange={e => setSearch(e.target.value)} /></div>
    {filtered.length ? <div className="upstreams-grid">{filtered.map(u => <section className={`panel upstream-card ${!u.enabled ? "disabled-card" : ""}`} key={u.id}><div className="upstream-card-top"><span className="upstream-avatar"><Server size={23} /></span><div><h2>{u.name}</h2><span><Globe2 size={12} />{u.region || "自定义区域"}</span></div><Switch aria-label={`${u.name} 启用状态`} checked={u.enabled} disabled={toggling === u.id} onCheckedChange={async value => {
      setToggling(u.id); try { await api(`/upstreams/${u.id}`, { method: "PUT", body: JSON.stringify({ ...upstreamPayload(u), enabled: value }) }); await reload(); notify(value ? "上游已启用" : "上游已停用") } catch (e) { notify(message(e), true) } finally { setToggling(null) }
    }} /></div><code className="upstream-url" title={u.base_url}>{u.base_url}</code><div className="upstream-meta"><div><span>鉴权方式</span><strong><ShieldCheck size={13} />{u.auth_mode === "api_key" ? "Bedrock API Key" : "AWS SigV4"}</strong></div><div><span>权重</span><strong>{u.weight}</strong></div><div><span>模型</span><strong>{Object.keys(u.models).length ? `${Object.keys(u.models).length} 个映射` : "全部透传"}</strong></div></div>{u.project && <p className="project-line">Project <code>{u.project}</code></p>}<div className="upstream-card-bottom"><HealthBadge upstream={u} health={health[u.id]} /><div><Button variant="ghost" size="sm" disabled={testing === u.id} onClick={async () => {
      setTesting(u.id); try { const result = await api<{ models: Model[] }>(`/upstreams/${u.id}/test`, { method: "POST" }); setModels({ name: u.name, data: result.models }); notify(`连接成功，发现 ${result.models.length} 个模型`) } catch (e) { notify(message(e), true) } finally { setTesting(null); await reload() }
    }}>{testing === u.id ? <Loader2 className="animate-spin" /> : <Radio />}检测</Button><Button variant="ghost" size="icon" aria-label={`编辑 ${u.name}`} onClick={() => setEditing(u)}><Edit3 /></Button><Confirm title={`删除「${u.name}」？`} description="删除后该上游将停止接收请求，绑定到这个上游的历史 Responses 将无法继续访问。" trigger={<Button variant="ghost" size="icon" className="danger-hover" aria-label={`删除 ${u.name}`}><Trash2 /></Button>} onConfirm={async () => { try { await api(`/upstreams/${u.id}`, { method: "DELETE" }); await reload(); notify("上游已删除") } catch (e) { notify(message(e), true) } }} /></div></div></section>)}</div> : <section className="panel"><Empty title={search ? "没有匹配的端点" : "连接你的第一个 Mantle 上游"} description={search ? "试试其他名称或区域。" : "准备好区域端点和 Bedrock API Key，即可开始接入。"} icon={<Server size={26} />} action={!search && <Button onClick={() => setEditing("new")}><Plus />添加上游端点</Button>} /></section>}
    <div className="tip-card"><CheckCircle2 size={18} /><p>上游凭据使用 AES-256-GCM 加密存储，管理接口不会返回明文。连接检测仅读取模型列表，不产生模型推理请求。</p><a href="https://docs.aws.amazon.com/bedrock/latest/userguide/bedrock-mantle.html" target="_blank" rel="noreferrer">AWS 文档<ArrowUpRight size={13} /></a></div>
    {editing && <UpstreamForm upstream={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} onSaved={reload} />}
    <Dialog open={!!models} onOpenChange={open => { if (!open) setModels(null) }}><DialogContent><DialogHeader><DialogTitle>连接检测成功</DialogTitle><DialogDescription>{models?.name} · 发现 {models?.data.length} 个模型</DialogDescription></DialogHeader><div className="discovered-models">{models?.data.map(m => <code key={m.id}>{m.id}</code>)}</div><DialogFooter><Button onClick={() => setModels(null)}>完成</Button></DialogFooter></DialogContent></Dialog>
  </>
}
