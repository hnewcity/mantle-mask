import { useEffect, useState } from "react"
import { Activity, ArrowDownLeft, ArrowUpRight, Download, RefreshCw, Zap } from "lucide-react"
import { Button } from "@/components/ui/button"
import { UsageChart } from "@/components/usage-chart"
import { UsagePeriod, type UsagePeriodValue } from "@/components/usage-period"
import type { UsageChartView } from "@/lib/usage-chart"
import { Select } from "@/components/ui/select"
import { SearchSelect } from "@/components/ui/search-select"
import { Empty, PageTitle, Spinner } from "@/components/common"
import { api, selfApi, message, type APIKey, type User, type Upstream, type UsageGroup, type UsageReport } from "@/lib/api"
import { number } from "@/lib/utils"
import { localDate } from "@/lib/dates"

function ModelUsage({ rows, selected, onSelect }: { rows: UsageGroup[]; selected: string; onSelect: (id: string) => void }) {
  const [expanded, setExpanded] = useState(false)
  const sorted = [...rows].sort((a, b) => b.total_tokens - a.total_tokens || b.requests - a.requests || a.id.localeCompare(b.id))
  const total = sorted.reduce((sum, row) => sum + row.total_tokens, 0)
  return <section className="panel model-usage" aria-label="模型用量">
    <div className="panel-heading"><div><h2>模型用量 <span className="count-label">{rows.length}</span></h2><p>当前筛选范围 · 点击模型筛选</p></div><span className="model-usage-unit">TOKEN 占比</span></div>
    {sorted.length ? <div className="model-usage-list">{(expanded ? sorted : sorted.slice(0, 5)).map(row => {
      const share = total ? row.total_tokens / total * 100 : 0
      return <button type="button" className="model-usage-row" key={row.id} aria-pressed={selected === row.id} disabled={!row.id}
        aria-label={`筛选模型 ${row.name || "未指定模型"}`} onClick={() => onSelect(selected === row.id ? "" : row.id)}>
        <span className="model-usage-title"><strong title={row.name}>{row.name || "未指定模型"}</strong><span>{number(row.total_tokens)} <small>{share.toFixed(1)}%</small></span></span>
        <span className="model-usage-bar" aria-hidden="true"><i style={{ width: `${share}%` }} /></span>
        <span className="model-usage-meta"><span>{number(row.requests)} 次请求</span><span>输入 {number(row.input_tokens)} · 输出 {number(row.output_tokens)}</span></span>
      </button>
    })}</div> : <Empty title="暂无模型用量" description="所选范围内的模型调用会汇总在这里。" />}
    {sorted.length > 5 && <Button variant="ghost" size="sm" className="model-usage-more" onClick={() => setExpanded(!expanded)}>{expanded ? "收起" : `查看全部 ${sorted.length} 个模型`}</Button>}
  </section>
}

function exportCSV(rows: UsageGroup[], label: string) {
  const cell = (v: string | number) => {
    let value = String(v)
    if (/^\s*[=+\-@]/.test(value)) value = `'${value}`
    return `"${value.replaceAll('"', '""')}"`
  }
  const header = ["名称", "ID", "请求", "失败", "输入 Token", "输出 Token", "总 Token", "缓存输入", "推理输出", "已返回用量", "未返回用量"]
  const lines = [header, ...rows.map(g => [g.name || "未指定", g.id, g.requests, g.errors, g.input_tokens, g.output_tokens, g.total_tokens, g.cached_tokens, g.reasoning_tokens, g.known_usage_requests, g.missing_usage_requests])]
  const url = URL.createObjectURL(new Blob(["\ufeff" + lines.map(line => line.map(cell).join(",")).join("\r\n")], { type: "text/csv;charset=utf-8" }))
  const a = document.createElement("a"); a.href = url; a.download = `mantle-usage-${label}-${localDate()}.csv`; a.click(); URL.revokeObjectURL(url)
}

export function Usage({ users = [], keys, upstreams = [], personal = false }: { users?: User[]; keys: APIKey[]; upstreams?: Upstream[]; personal?: boolean }) {
  const initialUser = personal ? "" : new URLSearchParams(location.hash.split("?")[1]).get("user_id") || ""
  const [userID, setUserID] = useState(initialUser), [keyID, setKeyID] = useState(""), [model, setModel] = useState("")
  const [upstreamID, setUpstreamID] = useState("")
  const [upstreamRows, setUpstreamRows] = useState<UsageGroup[]>([])
  const [from, setFrom] = useState(localDate(29)), [to, setTo] = useState(localDate()), [all, setAll] = useState(false)
  const [chartView, setChartView] = useState<UsageChartView>("tokens")
  const [group, setGroup] = useState<"by_user" | "by_key" | "by_model" | "by_upstream" | "daily">("by_model")
  const [modelRows, setModelRows] = useState<UsageGroup[]>([])
  const [report, setReport] = useState<UsageReport | null>(null), [error, setError] = useState(""), [busy, setBusy] = useState(false), [revision, setRevision] = useState(0)
  const baseQuery = new URLSearchParams({ timezone_offset: String(-new Date().getTimezoneOffset()), ...(all ? { all: "true" } : { from, to }), user_id: userID, key_id: keyID }).toString()
  useEffect(() => {
    const timer = setInterval(() => setRevision(v => v + 1), 15000)
    return () => clearInterval(timer)
  }, [])
  useEffect(() => {
    const controller = new AbortController()
    setBusy(true)
    const request = personal ? selfApi : api
    // Model choices and shares follow the selected upstream. Upstream choices
    // remain available when filtering, including deleted upstreams with history.
    const upstreamQuery = personal ? baseQuery : `${baseQuery}&upstream_id=${encodeURIComponent(upstreamID)}`
    void Promise.all([
      request<UsageReport>(`/usage?${upstreamQuery}&model=${encodeURIComponent(model)}`, { signal: controller.signal }),
      model ? request<UsageReport>(`/usage?${upstreamQuery}`, { signal: controller.signal }) : Promise.resolve(null),
      !personal && upstreamID ? request<UsageReport>(`/usage?${baseQuery}`, { signal: controller.signal }) : Promise.resolve(null),
    ])
      .then(([data, modelBaseline, upstreamBaseline]) => {
        if (!controller.signal.aborted) {
          setReport(data)
          setModelRows((modelBaseline || data).by_model)
          setUpstreamRows((upstreamBaseline || modelBaseline || data).by_upstream || [])
          setError("")
        }
      })
      .catch(e => { if (!controller.signal.aborted) { setError(message(e)); setReport(null); setModelRows([]); setUpstreamRows([]) } })
      .finally(() => { if (!controller.signal.aborted) setBusy(false) })
    return () => controller.abort()
  }, [baseQuery, model, upstreamID, revision, personal])
  const upstreamLabels = new Map(upstreamRows.filter(row => row.id).map(row => [row.id, `${row.name || row.id}（已删除）`]))
  for (const upstream of upstreams) upstreamLabels.set(upstream.id, `${upstream.name}${upstream.enabled ? "" : "（已停用）"}`)
  const upstreamName = upstreamLabels.get(upstreamID) || upstreamID
  const rows = (report?.[group] || []).map(g => ({
    ...g, name: group === "by_user" ? users.find(u => u.id === g.id)?.name || g.name :
      group === "by_key" ? `${g.name}${keys.find(k => k.id === g.id)?.revoked_at ? "（已撤销）" : ""}` :
      group === "by_upstream" ? upstreamLabels.get(g.id) || g.name || (g.id ? g.id : "未记录上游") : g.name,
  }))
  const totals = report?.totals
  const period = { from, to, all }
  function changePeriod(value: UsagePeriodValue) {
    setFrom(value.from)
    setTo(value.to)
    setAll(value.all)
  }
  const modelOptions = [{ value: "", label: "全部模型" }, ...modelRows.filter(row => row.id).map(row => ({ value: row.id, label: row.name || row.id }))]
  // Retain a selected model even when a new date/upstream range has no calls.
  if (model && !modelOptions.some(option => option.value === model)) modelOptions.push({ value: model, label: model })
  const filteredKeys = keys.filter(k => !userID || k.user_id === userID)
  return <>
    <PageTitle eyebrow={personal ? "YOUR API ACTIVITY" : "USAGE ANALYTICS"} title={personal ? "我的用量" : "用量统计"} description={personal ? "查看自己名下全部 Key 的调用与 Token 用量。" : "按上游、用户、密钥、模型和日期查看历史用量。数据持续保存，重启后仍可查询。"} action={<Button variant="outline" disabled={busy} onClick={() => setRevision(v => v + 1)}><RefreshCw className={busy ? "animate-spin" : ""} />刷新用量</Button>} />
    <section className="panel usage-filters" aria-label="用量筛选">
      <UsagePeriod value={period} onChange={changePeriod} />
      <div className="usage-dimensions">
        {!personal && <label>用户<Select aria-label="筛选用户" value={userID} onValueChange={value => { setUserID(value); setKeyID("") }} options={[{ value: "", label: "全部用户" }, ...users.map(u => ({ value: u.id, label: `${u.name}${!u.enabled ? "（已停用）" : ""}` })), { value: "system-admin", label: "管理员 Playground" }]} /></label>}
        <label>API Key<Select aria-label="筛选密钥" value={keyID} onValueChange={setKeyID} options={[{ value: "", label: "全部密钥" }, ...filteredKeys.map(k => ({ value: k.id, label: `${k.name} · ${k.prefix}${k.revoked_at ? "（已撤销）" : ""}` }))]} /></label>
        {!personal && <label>上游<SearchSelect aria-label="筛选上游" value={upstreamID} onValueChange={setUpstreamID} searchPlaceholder="搜索上游名称…" emptyMessage="没有匹配的上游" options={[{ value: "", label: "全部上游" }, ...Array.from(upstreamLabels, ([value, label]) => ({ value, label }))]} /></label>}
        <label>模型<SearchSelect aria-label="筛选模型" value={model} onValueChange={setModel} searchPlaceholder="搜索已调用的模型…" emptyMessage="此范围内没有匹配的模型" options={modelOptions} /></label>
        <span className="usage-timezone">日期按本机时区（UTC{new Date().getTimezoneOffset() <= 0 ? "+" : ""}{-new Date().getTimezoneOffset() / 60}）</span>
      </div>
      {!personal && upstreamID && <div className="usage-active-filter"><span>正在查看上游 <strong>{upstreamName}</strong> 的用量</span><Button variant="ghost" size="sm" onClick={() => setUpstreamID("")}>清除上游筛选</Button></div>}
      {model && <div className="usage-active-filter"><span>正在查看 <strong>{model}</strong> 的用量</span><Button variant="ghost" size="sm" onClick={() => setModel("")}>清除模型筛选</Button></div>}
    </section>
    {error && <div className="error-banner" role="alert">{error}</div>}
    {!report && !error ? <Spinner label="读取历史用量…" /> : totals && report && <>
      <div className="stats-grid usage-stats">{[
        { title: "总 Token", value: totals.total_tokens, hint: "输入 + 输出，不重复累加子项", icon: Zap },
        { title: "输入 Token", value: totals.input_tokens, hint: `已报告缓存输入 ${number(totals.cached_tokens)}`, icon: ArrowDownLeft },
        { title: "输出 Token", value: totals.output_tokens, hint: `已报告推理输出 ${number(totals.reasoning_tokens)}`, icon: ArrowUpRight },
        { title: "请求数", value: totals.requests, hint: `${number(totals.errors)} 次失败 · ${number(totals.in_flight_requests)} 次进行中`, icon: Activity },
      ].map(card => <div className="stat-card" key={card.title}><div className="stat-heading"><span>{card.title}</span><card.icon size={18} /></div><div className="stat-value">{number(card.value)}</div><div className="stat-label">{card.hint}</div></div>)}</div>
      {totals.missing_usage_requests > 0 && <div className="usage-warning" role="status"><strong>{number(totals.missing_usage_requests)} 次请求未返回用量</strong><span>可能是上游未提供、请求失败、流中断或后台任务尚未完成。下方 Token 仅汇总实际返回值，不将未知用量当作零。</span></div>}
      <div className="usage-visuals">
        <ModelUsage rows={modelRows} selected={model} onSelect={setModel} />
        <section className="panel usage-daily" aria-label="每日 Token 用量" aria-busy={busy}>
          <div className="panel-heading"><div><h2>每日 Token 用量</h2><p>{report.all ? "全部历史" : `${report.from} — ${report.to}`} · 最多展示范围内最后 90 天</p></div></div>
          <div className="usage-chart-controls">
            <div className="usage-chart-dimensions">
              <div className="usage-chart-view" role="group" aria-label="图表分组">
                <Button type="button" variant={chartView === "tokens" ? "secondary" : "ghost"} size="sm" aria-pressed={chartView === "tokens"} onClick={() => setChartView("tokens")}>输入 / 输出</Button>
                <Button type="button" variant={chartView === "models" ? "secondary" : "ghost"} size="sm" aria-pressed={chartView === "models"} onClick={() => setChartView("models")}>按模型</Button>
              </div>
              <SearchSelect aria-label="图表筛选模型" value={model} onValueChange={setModel} options={modelOptions} searchPlaceholder="搜索已调用的模型…" emptyMessage="此范围内没有匹配的模型" />
            </div>
            <UsagePeriod value={period} onChange={changePeriod} compact />
            <p className="usage-chart-scope">模型和日期筛选同步应用于整页统计与明细{busy ? " · 更新中…" : ""}</p>
          </div>
          <UsageChart report={report} view={chartView} onSelectModel={value => setModel(value === model ? "" : value)} />
        </section>
      </div>
      <section className="panel usage-breakdown">
        <div className="panel-heading"><div className="usage-tabs" role="tablist" aria-label="用量分组">{([["by_model", "按模型"], ["by_upstream", "按上游"], ["by_user", "按用户"], ["by_key", "按密钥"], ["daily", "按日期"]] as const).filter(([id]) => !personal || (id !== "by_user" && id !== "by_upstream")).map(([id, label]) => <button role="tab" aria-selected={group === id} key={id} className={group === id ? "active" : ""} onClick={() => setGroup(id)}>{label}</button>)}</div><Button variant="ghost" size="sm" disabled={!rows.length || busy} onClick={() => exportCSV(rows, group)}><Download size={14} />导出 CSV</Button></div>
        {rows.length ? <div className="table-wrap"><table><thead><tr><th>{group === "daily" ? "日期" : "名称"}</th><th>请求 / 失败</th><th>输入</th><th>输出</th><th>总 Token</th><th>缓存 / 推理</th><th>未返回用量</th></tr></thead><tbody>{rows.map(g => <tr key={g.id}><td><strong className="usage-group-name" title={g.name}>{g.name || "未指定模型"}</strong>{group === "by_key" && <small>{keys.find(k => k.id === g.id)?.prefix || g.id}</small>}{group === "by_upstream" && <small>{g.id || "旧记录或未发往上游的请求"}</small>}</td><td className="mono">{number(g.requests)}<small>{number(g.errors)} 次失败</small></td><td className="mono">{number(g.input_tokens)}</td><td className="mono">{number(g.output_tokens)}</td><td className="mono"><strong>{number(g.total_tokens)}</strong></td><td className="mono">{number(g.cached_tokens)} / {number(g.reasoning_tokens)}</td><td className={g.missing_usage_requests ? "amber" : "muted"}>{number(g.missing_usage_requests)}</td></tr>)}</tbody></table></div> : <Empty title="暂无用量明细" description="试试其他日期或筛选条件。" />}
      </section>
      {!personal && <p className="usage-footnote">按上游统计将每次请求归属到最后一次尝试的上游，重试不重复计数。升级前的旧记录和未发往上游的请求显示为“未记录上游”；删除上游不会删除历史用量。</p>}
      <p className="usage-footnote">已收到 {number(totals.known_usage_requests)} 次请求的用量。从启用此版本起记录。统计依据上游返回的 usage，不估算 Token，也不等同于 AWS 账单；缓存和推理明细仅汇总已报告值，已包含在输入、输出中。模型列表与 Responses 查询不计为新的推理请求。</p>
    </>}
  </>
}
