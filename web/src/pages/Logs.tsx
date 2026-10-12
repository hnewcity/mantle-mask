import { useCallback, useEffect, useState } from "react"
import { Download, RefreshCw, Search } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
import { LogsTable, notify, PageTitle, Spinner } from "@/components/common"
import { api, message, type RequestLog } from "@/lib/api"

export function Logs() {
  const [logs, setLogs] = useState<RequestLog[] | null>(null), [query, setQuery] = useState(""), [status, setStatus] = useState("all"), [busy, setBusy] = useState(false)
  const load = useCallback(async () => {
    setBusy(true)
    try { const response = await api<{ data: RequestLog[] }>("/logs"); setLogs(response.data) }
    catch (e) { notify(message(e), true) } finally { setBusy(false) }
  }, [])
  useEffect(() => { void load(); const timer = setInterval(() => void load(), 15000); return () => clearInterval(timer) }, [load])
  const filtered = (logs || []).filter(log =>
    (status === "all" || (status === "success" ? log.status < 400 : log.status >= 400)) &&
    `${log.id} ${log.model} ${log.upstream_name} ${log.path} ${log.key_name} ${log.user_name}`.toLowerCase().includes(query.toLowerCase()))
  return <><PageTitle eyebrow="REQUEST OBSERVABILITY" title="请求日志" description="查看每次请求的去向和结果。仅记录元数据，不保存提示词或响应正文。" action={<Button variant="outline" disabled={!filtered.length} onClick={() => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(filtered, null, 2)], { type: "application/json" }))
    const link = document.createElement("a"); link.href = url; link.download = `mantle-logs-${new Date().toISOString().slice(0, 10)}.json`; link.click(); URL.revokeObjectURL(url)
  }}><Download />导出 JSON</Button>} /><section className="panel"><div className="log-toolbar"><div className="search-field"><Search size={16} /><Input aria-label="搜索请求日志" placeholder="搜索模型、上游、密钥或请求 ID…" value={query} onChange={e => setQuery(e.target.value)} /></div><Select aria-label="状态筛选" value={status} onValueChange={setStatus} options={[{ value: "all", label: "全部状态" }, { value: "success", label: "成功" }, { value: "error", label: "失败" }]} /><Button variant="ghost" size="icon" aria-label="刷新日志" disabled={busy} onClick={() => void load()}><RefreshCw className={busy ? "animate-spin" : ""} /></Button></div>{logs === null ? <Spinner /> : <LogsTable logs={filtered} />}<div className="panel-footer"><span>显示 {filtered.length} / {logs?.length || 0} 条记录</span><span>每 15 秒刷新 · 日志保留在内存中，重启清空</span></div></section><p className="page-note">流式请求若在响应头发送后失败，客户端 HTTP 状态可能仍为 200；这里记录流的最终结果。Token 只统计上游明确返回的用量。</p></>
}
