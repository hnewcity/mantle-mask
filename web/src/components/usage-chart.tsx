import { useMemo, useState } from "react"
import { BarChart3 } from "lucide-react"
import { Empty } from "@/components/common"
import type { UsageReport } from "@/lib/api"
import { number } from "@/lib/utils"
import { buildUsageChart, type UsageChartView } from "@/lib/usage-chart"

export function UsageChart({ report, view, onSelectModel }: { report: UsageReport; view: UsageChartView; onSelectModel: (model: string) => void }) {
  const { series, points } = useMemo(() => buildUsageChart(report, view), [report, view])
  const [activeDate, setActiveDate] = useState<string | null>(null)
  if (view === "models" && !report.daily_by_model) return <Empty title="暂无每日模型明细" description="请升级网关服务以查看按模型的每日用量。" />
  if (!points.length) return <Empty title="所选范围内暂无调用" description="使用用户的 API Key 发起模型请求后，用量会自动出现在这里。" icon={<BarChart3 size={24} />} />
  const peak = Math.max(1, ...points.map(p => p.input + p.output)), step = 700 / points.length, width = Math.min(38, step * .65)
  const active = points.find(point => point.id === activeDate)
  function description(point: typeof points[number]) {
    return `${point.id} · 输入 ${number(point.input)} · 输出 ${number(point.output)}${view === "models" ? series.map((s, i) => ` · ${s.label} ${number(point.values[i])}`).join("") : ""}${point.missing ? ` · ${number(point.missing)} 次请求未返回用量` : ""}`
  }
  return <>
    <div className="usage-legend" aria-label={view === "models" ? "模型图例" : "Token 图例"}>
      {series.map(s => s.model ? <button type="button" key={s.id} title={`筛选模型 ${s.label}`} onClick={() => onSelectModel(s.model!)}>
        <i style={{ background: s.color }} /><span>{s.label}</span>
      </button> : <span key={s.id} title={s.label}><i style={{ background: s.color }} /><span>{s.label}</span></span>)}
    </div>
    <div className="usage-chart" onMouseLeave={() => setActiveDate(null)}>
      <div className="usage-chart-scale"><span>{number(peak)} tokens</span><span>0</span></div>
      <svg viewBox="0 0 700 170" preserveAspectRatio="none" role="img" aria-label={`每日${view === "models" ? "按模型区分的" : "输入和输出"} Token 用量，最多展示最近 90 天`}>
        {[20, 85, 150].map(y => <line key={y} x1="0" x2="700" y1={y} y2={y} stroke="var(--border)" strokeDasharray="3 5" />)}
        {points.map((point, i) => {
          let top = 150
          return <g key={point.id} tabIndex={0} aria-label={description(point)}
            onMouseEnter={() => setActiveDate(point.id)} onFocus={() => setActiveDate(point.id)} onBlur={() => setActiveDate(null)}>
            <title>{description(point)}</title>
            <rect x={i * step} y="0" width={step} height="170" fill={active === point ? "var(--chart-highlight)" : "transparent"} />
            {point.values.map((value, index) => {
              const height = value / peak * 130
              top -= height
              return <rect key={series[index].id} x={(i + .5) * step - width / 2} y={top} width={width} height={height} fill={series[index].color} />
            })}
          </g>
        })}
      </svg>
      <div className="usage-chart-dates"><span>{points[0].id}</span><span>{points.at(-1)!.id}</span></div>
    </div>
    <p className="usage-chart-detail" aria-live="polite">{active ? description(active) : view === "models" ? "悬停或聚焦查看每日明细 · 点击图例筛选模型" : "悬停或聚焦查看每日输入和输出"}</p>
  </>
}
