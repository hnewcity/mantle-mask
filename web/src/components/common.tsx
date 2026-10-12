import { useEffect, useId, useState, type ReactNode } from "react"
import { Check, CheckCircle2, Copy, Loader2, X, AlertCircle, ArrowUpRight, Layers3 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import type { Health, RequestLog, Upstream } from "@/lib/api"
import { cn, number, time } from "@/lib/utils"

export function notify(text: string, error = false) {
  window.dispatchEvent(new CustomEvent("mantle:toast", { detail: { text, error } }))
}
export function Toast() {
  const [toast, setToast] = useState<{ text: string; error: boolean } | null>(null)
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>
    const handler = (event: Event) => {
      setToast((event as CustomEvent).detail)
      clearTimeout(timer)
      timer = setTimeout(() => setToast(null), 6000)
    }
    window.addEventListener("mantle:toast", handler)
    return () => { clearTimeout(timer); window.removeEventListener("mantle:toast", handler) }
  }, [])
  if (!toast) return null
  return <div role="status" className={cn("toast", toast.error && "toast-error")}>
    {toast.error ? <AlertCircle size={18} /> : <CheckCircle2 size={18} />}
    <span>{toast.text}</span><button aria-label="关闭通知" onClick={() => setToast(null)}><X size={16} /></button>
  </div>
}
export function CopyButton({ value, label = "复制", className }: { value: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  return <Button type="button" variant="ghost" size="sm" className={className} aria-label={label} onClick={async () => {
    try { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 2000) }
    catch { notify("复制失败，请手动选择文本复制", true) }
  }}>{copied ? <Check size={14} /> : <Copy size={14} />}<span>{copied ? "已复制" : label}</span></Button>
}
export function Brand({ small = false }: { small?: boolean }) {
  return <div className={cn("brand", small && "brand-small")}><svg className="brand-mark" viewBox="0 0 36 36" aria-hidden="true"><path d="M3 29V7l7.5 11L18 7v22M18 29V7l7.5 11L33 7v22" /></svg><div><strong>mantle mask</strong>{!small && <p>THE CONNECTED INTELLIGENCE</p>}</div></div>
}
export function PageTitle({ eyebrow, title, description, action }: { eyebrow: string; title: string; description: string; action?: ReactNode }) {
  return <div className="page-title"><div><p className="eyebrow">{eyebrow}</p><h1>{title}</h1><p>{description}</p></div>{action}</div>
}
export function Empty({ title, description, action, icon }: { title: string; description: string; action?: ReactNode; icon?: ReactNode }) {
  return <div className="empty"><div className="empty-icon">{icon || <Layers3 size={23} />}</div><h3>{title}</h3><p>{description}</p>{action}</div>
}
export function Spinner({ label = "加载中…" }: { label?: string }) {
  return <div className="loading"><Loader2 className="animate-spin" size={20} />{label}</div>
}
export function HealthBadge({ upstream, health }: { upstream: Upstream; health?: Health }) {
  const activeCooldown = health?.status === "cooldown" && new Date(health.cooldown_until) > new Date()
  const [label, style] = !upstream.enabled ? ["已停用", "neutral"] :
    activeCooldown ? ["冷却中", "amber"] :
    health?.status === "healthy" ? ["连接正常", "green"] :
    health?.status === "error" ? ["鉴权异常", "red"] :
    health?.status === "reachable" ? ["可连接", "green"] : ["待检测", "neutral"]
  return <span className={cn("status", style)}><i />{label}</span>
}
export function Field({ label, hint, children, className }: { label: string; hint?: string; children: ReactNode; className?: string }) {
  const id = useId()
  // Wrapping label associates the native input; hint remains readable below it.
  return <div className={cn("field", className)}><label id={id}><span>{label}</span>{children}</label>{hint && <p>{hint}</p>}</div>
}
export function Confirm({ title, description, trigger, onConfirm }: { title: string; description: string; trigger: ReactNode; onConfirm: () => Promise<void> }) {
  const [open, setOpen] = useState(false), [busy, setBusy] = useState(false)
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild>{trigger}</DialogTrigger><DialogContent className="sm:max-w-md">
    <DialogHeader><DialogTitle>{title}</DialogTitle><DialogDescription>{description}</DialogDescription></DialogHeader>
    <DialogFooter><Button variant="outline" onClick={() => setOpen(false)}>取消</Button><Button variant="destructive" disabled={busy} onClick={async () => {
      setBusy(true); try { await onConfirm(); setOpen(false) } finally { setBusy(false) }
    }}>{busy && <Loader2 className="animate-spin" />}确认</Button></DialogFooter>
  </DialogContent></Dialog>
}
export function LogsTable({ logs, compact = false }: { logs: RequestLog[]; compact?: boolean }) {
  if (!logs.length) return <Empty title="等待第一个请求" description="通过 API 或 Playground 发起请求后，这里会显示实际调用记录。" />
  return <div className="table-wrap"><table><thead><tr><th>时间 / 请求</th><th>模型</th><th>上游</th><th>状态</th><th>耗时</th>{!compact && <><th>Token</th><th>尝试 / 用户 / Key</th></>}</tr></thead><tbody>
    {logs.map(log => <tr key={log.id}><td><span className="mono">{time(log.time)}</span><small title={log.id}>{log.path.replace("/v1/", "")}{log.stream && <span className="stream-dot">SSE</span>}</small></td><td><span className="model-name" title={log.model}>{log.model || "—"}</span></td><td>{log.upstream_name || "—"}</td><td><Badge variant="outline" className={log.status < 400 ? "badge-green" : "badge-red"} title={log.error}>{log.status < 400 ? <Check size={11} /> : <AlertCircle size={11} />}{log.status}</Badge></td><td className="mono">{number(log.duration_ms)}<span className="muted"> ms</span></td>{!compact && <><td className="mono">{!log.inference ? "—" : log.usage_known ? number(log.input_tokens + log.output_tokens) : "未返回"}</td><td>{log.attempts}<small>{log.user_name} · {log.key_name}</small></td></>}</tr>)}
  </tbody></table></div>
}
export function LinkButton({ href, children }: { href: string; children: ReactNode }) {
  return <a className="text-link" href={href}>{children}<ArrowUpRight size={14} /></a>
}
