import { useEffect, useId, useState, type FormEvent } from "react"
import { DollarSign, Loader2, Plus, RefreshCw, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { api, message, type ModelPrices, type ModelPrice, type PriceCatalog } from "@/lib/api"
import { notify } from "@/components/common"
import { formatTokenPrice } from "@/lib/usage-cost"

type Advanced = Pick<ModelPrice, "long_context_threshold" | "long_input_usd_per_million" | "long_output_usd_per_million" | "long_cached_input_usd_per_million">
type PriceRow = { model: string; input: string; output: string; cached: string; advanced?: Advanced }
const sourceNames: Record<string, string> = { aws: "AWS 官方", litellm: "LiteLLM", "models.dev": "models.dev", "aws+litellm": "AWS + LiteLLM" }
const blank = (): PriceRow => ({ model: "", input: "", output: "", cached: "" })

export function UsagePricing({ models, onSaved }: { models: string[]; onSaved: () => void }) {
  const [open, setOpen] = useState(false), [loading, setLoading] = useState(false), [saving, setSaving] = useState(false), [loaded, setLoaded] = useState(false), [syncing, setSyncing] = useState(false)
  const [rows, setRows] = useState<PriceRow[]>([]), [error, setError] = useState("")
  const [catalog, setCatalog] = useState<PriceCatalog | null>(null), [search, setSearch] = useState("")
  const suggestions = useId()
  const references = (catalog?.entries || []).filter(entry => `${entry.model} ${entry.region} ${sourceNames[entry.source]}`.toLowerCase().includes(search.trim().toLowerCase()))
  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    setLoading(true)
    setLoaded(false)
    setRows([])
    setError("")
    void Promise.all([
      api<ModelPrices>("/usage/pricing", { signal: controller.signal }),
      api<PriceCatalog>("/usage/pricing/reference", { signal: controller.signal }),
    ]).then(([prices, reference]) => {
      if (!controller.signal.aborted) {
        setLoaded(true)
        setCatalog(reference)
        setRows(Object.entries(prices).sort(([a], [b]) => a.localeCompare(b)).map(([model, price]) => ({
          model, input: String(price.input_usd_per_million), output: String(price.output_usd_per_million), cached: price.cached_input_usd_per_million === undefined ? "" : String(price.cached_input_usd_per_million),
          advanced: { long_context_threshold: price.long_context_threshold, long_input_usd_per_million: price.long_input_usd_per_million, long_output_usd_per_million: price.long_output_usd_per_million, long_cached_input_usd_per_million: price.long_cached_input_usd_per_million },
        })))
      }
    }).catch(e => { if (!controller.signal.aborted) setError(message(e)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [open])
  function change(index: number, field: "model" | "input" | "output" | "cached", value: string) {
    setRows(current => current.map((row, i) => i === index ? { ...row, [field]: value } : row))
  }
  async function save(event: FormEvent) {
    event.preventDefault()
    const entries = rows.map(row => [row.model.trim(), {
      input_usd_per_million: Number(row.input), output_usd_per_million: Number(row.output),
      ...(row.cached === "" ? {} : { cached_input_usd_per_million: Number(row.cached) }), ...row.advanced,
    }] as const)
    if (new Set(entries.map(([model]) => model)).size !== entries.length) {
      setError("每个模型只能配置一组单价，请删除重复项。")
      return
    }
    setSaving(true)
    setError("")
    try {
      await api("/usage/pricing", { method: "PUT", body: JSON.stringify(Object.fromEntries(entries)) })
      notify("模型单价已保存")
      onSaved()
      setOpen(false)
    } catch (e) { setError(message(e)) }
    finally { setSaving(false) }
  }
  async function synchronize() {
    setSyncing(true)
    setError("")
    try {
      const data = await api<PriceCatalog>("/usage/pricing/sync", { method: "POST", body: "{}" })
      setCatalog(data)
      notify("参考价已同步，手动覆盖保持不变")
      onSaved()
    } catch (e) { setError(`${message(e)} 原有参考价格和手动覆盖未被清空。`) }
    finally { setSyncing(false) }
  }
  return <Dialog open={open} onOpenChange={value => { if (!saving && !syncing) setOpen(value) }}>
    <DialogTrigger asChild><Button variant="outline"><DollarSign />配置模型单价</Button></DialogTrigger>
    <DialogContent className="usage-pricing-dialog">
      <DialogHeader><DialogTitle>模型单价</DialogTitle><DialogDescription>同步公开参考价，或按控制台记录的模型 ID / 别名设置手动覆盖。单位为 USD / 百万 Token；所有用户共享，历史金额按当前配置重新估算。</DialogDescription></DialogHeader>
      <p className="usage-pricing-note">参考价按上游区域和当前实际模型映射匹配，仅采用 Standard 档位；手动覆盖优先，并对同名模型跨上游生效。缓存单价缺失时按输入单价估算，推理不重复收费。不含 Flex / Priority、批量折扣、缓存写入、工具费或税费。</p>
      {error && <div className="inline-error" role="alert">{error}</div>}
      {loading ? <p role="status">正在读取模型单价…</p> : loaded && <>
        <section className="usage-reference" aria-label="参考价格">
          <div className="usage-reference-heading"><div><h3>参考价格 <span className="count-label">{catalog?.entries.length || 0}</span></h3><p>{catalog?.entries.length ? `最近同步：${new Date(catalog.updated_at).toLocaleString("zh-CN")}` : "尚未同步公开价格"}</p></div><Button type="button" variant="outline" disabled={saving || syncing} onClick={() => void synchronize()}><RefreshCw className={syncing ? "animate-spin" : ""} />{syncing ? "同步中…" : "同步参考价格"}</Button></div>
          <p className="usage-pricing-note">来源优先级：AWS 官方 → LiteLLM → models.dev。社区数据可能滞后；缺少可信匹配时不估算。同步不需要 AWS 凭据，也不会发起模型推理。</p>
          {!!catalog?.warnings.length && <div className="usage-reference-warning" role="status">{catalog.warnings.map((warning, i) => <p key={i}>{warning}</p>)}</div>}
          {!!catalog?.entries.length && <><Input aria-label="搜索参考价格" placeholder="搜索模型、区域或价格来源…" value={search} onChange={e => setSearch(e.target.value)} />
            <div className="usage-reference-list">{references.slice(0, 30).map(entry => <div className="usage-reference-row" key={`${entry.region}/${entry.model}`}>
              <div><strong title={entry.model}>{entry.model}</strong><small>{entry.region} · {sourceNames[entry.source]} · {new Date(entry.updated_at).toLocaleDateString("zh-CN")}</small></div>
              <span>输入 {formatTokenPrice(entry.price.input_usd_per_million)} · 输出 {formatTokenPrice(entry.price.output_usd_per_million)} · 缓存 {formatTokenPrice(entry.price.cached_input_usd_per_million)}
                {!!entry.price.long_context_threshold && <small>输入超过 {entry.price.long_context_threshold.toLocaleString("zh-CN")} Token：输入 {formatTokenPrice(entry.price.long_input_usd_per_million)} · 输出 {formatTokenPrice(entry.price.long_output_usd_per_million)} · 缓存 {entry.price.long_cached_input_usd_per_million === undefined ? "同长上下文输入" : formatTokenPrice(entry.price.long_cached_input_usd_per_million)}</small>}</span>
            </div>)}{!references.length && <p className="usage-pricing-note">没有匹配的参考价格。</p>}{references.length > 30 && <p className="usage-pricing-note">展示前 30 / {references.length} 条，请搜索缩小范围。</p>}</div>
          </>}
        </section>
        <form onSubmit={save}>
        <fieldset disabled={saving || syncing} className="usage-pricing-fields">
          <h3 className="usage-price-heading">手动覆盖</h3>
          <datalist id={suggestions}>{models.map(model => <option key={model} value={model} />)}</datalist>
          <div className="usage-pricing-list">
            {!rows.length && <p className="usage-pricing-note">尚未配置单价。将自动使用可匹配的参考价格，没有匹配的模型仍显示为「未估算」，不会当作免费用量。</p>}
            {rows.map((row, index) => <div className="usage-price-row" key={index}>
              <label>模型 ID / 别名<Input aria-label={`模型 ${index + 1}`} list={suggestions} required maxLength={256} value={row.model} onChange={e => change(index, "model", e.target.value)} placeholder="与用量明细中的 ID 一致" />{!!row.advanced?.long_context_threshold && <small>已保留 {row.advanced.long_context_threshold.toLocaleString("zh-CN")} Token 阶梯配置</small>}</label>
              {([["input", "输入单价"], ["output", "输出单价"], ["cached", "缓存输入单价"]] as const).map(([field, label]) => <label key={field}>{label}<Input aria-label={`${label} ${index + 1}`} type="number" required={field !== "cached"} min={0} max={1000000} step="any" value={row[field]} onChange={e => change(index, field, e.target.value)} placeholder={field === "cached" ? "同输入" : "USD / 百万"} /></label>)}
              <Button type="button" variant="ghost" aria-label={`删除模型单价 ${index + 1}`} onClick={() => setRows(current => current.filter((_, i) => i !== index))}><Trash2 size={16} /></Button>
            </div>)}
          </div>
          <Button type="button" variant="ghost" size="sm" disabled={rows.length >= 200} onClick={() => setRows(current => [...current, blank()])}><Plus size={14} />添加模型单价</Button>
          <DialogFooter><Button type="button" variant="outline" onClick={() => setOpen(false)}>取消</Button><Button disabled={saving}>{saving && <Loader2 className="animate-spin" />}保存单价</Button></DialogFooter>
        </fieldset>
      </form></>}
    </DialogContent>
  </Dialog>
}
