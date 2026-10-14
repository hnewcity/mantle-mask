import { useState } from "react"
import { Clock3, HardDrive, Loader2, Save, ShieldCheck, Workflow } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Field, notify, PageTitle } from "@/components/common"
import { BackupSettings } from "@/components/backup-settings"
import { api, message, type Settings } from "@/lib/api"

export function SettingsPage({ settings, reload }: { settings: Settings; reload: () => Promise<void> }) {
  const [form, setForm] = useState(settings), [busy, setBusy] = useState(false)
  const [migrationBusy, setMigrationBusy] = useState(false)
  const change = (key: keyof Settings, value: number | boolean) => setForm(prev => ({ ...prev, [key]: value }))
  return <><PageTitle eyebrow="GATEWAY CONFIGURATION" title="网关设置" description="调整请求行为和运行参数。保存后对新请求即时生效。" /><form className="settings-form" onSubmit={async event => { event.preventDefault(); if (migrationBusy) return; setBusy(true); try { await api("/settings", { method: "PUT", body: JSON.stringify(form) }); await reload(); notify("网关设置已保存") } catch (e) { notify(message(e), true) } finally { setBusy(false) } }}><fieldset className="settings-fields" disabled={busy || migrationBusy}>
    <section className="panel settings-section"><div className="settings-section-title"><span className="section-icon"><Workflow size={21} /></span><div><h2>请求与路由</h2><p>控制完整请求时限、重试和故障冷却</p></div></div><div className="form-grid"><Field label="请求总超时（秒）" hint="5–3600 秒；覆盖重试及完整流式响应"><Input type="number" required min={5} max={3600} value={form.request_timeout_seconds} onChange={e => change("request_timeout_seconds", Number(e.target.value))} /></Field><Field label="最大重试次数" hint="0–5 次；每个上游最多尝试一次"><Input type="number" required min={0} max={5} value={form.max_retries} onChange={e => change("max_retries", Number(e.target.value))} /></Field><Field label="上游冷却时间（秒）" hint="1–600 秒；临时故障后暂停参与新请求路由"><Input type="number" required min={1} max={600} value={form.cooldown_seconds} onChange={e => change("cooldown_seconds", Number(e.target.value))} /></Field></div><div className="inline-tip"><Clock3 size={16} />上游响应头固定等待最多 60 秒；流开始后不会重试。</div></section>
    <section className="panel settings-section"><div className="settings-section-title"><span className="section-icon"><HardDrive size={21} /></span><div><h2>记录与存储</h2><p>配置持久化保存，请求日志保留在内存中</p></div></div><Field label="最大日志条数" hint="100–10000 条；达到上限后移除最早的记录"><Input className="max-w-xs" type="number" required min={100} max={10000} value={form.log_capacity} onChange={e => change("log_capacity", Number(e.target.value))} /></Field><div className="storage-info"><span><ShieldCheck size={15} />上游凭据：AES-256-GCM</span><span><HardDrive size={15} />配置与 Responses 路由：本地文件</span></div></section>
    <section className="panel settings-section"><div className="settings-section-title"><span className="section-icon"><ShieldCheck size={21} /></span><div><h2>连接安全</h2><p>根据部署环境控制上游连接方式</p></div></div><div className="toggle-row"><div><strong>允许非本机 HTTP 上游</strong><p>用于可信内网中的代理端点。默认只允许 HTTPS 和本机回环 HTTP。</p></div><Switch aria-label="允许非本机 HTTP 上游" checked={form.allow_http_upstreams} onCheckedChange={v => change("allow_http_upstreams", v)} /></div></section><div className="settings-actions"><span>配置保存到服务端的数据目录</span><Button disabled={busy}>{busy ? <Loader2 className="animate-spin" /> : <Save />}保存设置</Button></div>
  </fieldset></form><div className="settings-form backup-settings"><BackupSettings disabled={busy} onBusyChange={setMigrationBusy} onRestored={async () => { const restored = await api<Settings>("/settings"); setForm(restored); await reload() }} /></div></>
}
