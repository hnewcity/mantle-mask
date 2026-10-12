import type { UsageReport } from "@/lib/api"

export type UsageChartView = "tokens" | "models"
export type UsageChartSeries = { id: string; label: string; color: string; model?: string }
// Share the console theme rather than introducing a separate chart palette.
const modelColors = Array.from({ length: 6 }, (_, index) => `var(--chart-model-${index + 1})`)

export function buildUsageChart(report: UsageReport, view: UsageChartView) {
  if (!report.daily.length) return { series: [], points: [] }
  const dates = report.daily.map(row => row.id).sort()
  const end = report.all ? dates.at(-1)! : report.to
  const start = report.all ? dates[0] : report.from
  const last = Date.parse(`${end}T00:00:00Z`)
  const first = Math.max(Date.parse(`${start}T00:00:00Z`), last - 89 * 86400000)
  const firstDate = new Date(first).toISOString().slice(0, 10)
  const modelRows = (report.daily_by_model || []).filter(row => row.date >= firstDate && row.date <= end)
  const totals = new Map<string, number>()
  for (const row of modelRows) totals.set(row.model, (totals.get(row.model) || 0) + row.total_tokens)
  const models = [...totals].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
  // Keep the legend readable without dropping smaller models' token counts.
  const visible = models.slice(0, models.length > 6 ? 5 : 6)
  const series: UsageChartSeries[] = view === "tokens" ? [
    { id: "input", label: "输入", color: "var(--chart-input)" },
    { id: "output", label: "输出", color: "var(--chart-output)" },
  ] : visible.map(([model], index) => ({ id: `model:${model}`, model, label: model || "未指定模型", color: modelColors[index] }))
  if (view === "models" && models.length > 6) series.push({ id: "other", label: `其他 ${models.length - 5} 个模型`, color: modelColors[5] })
  const daily = new Map(report.daily.map(row => [row.id, row]))
  const modelIndex = new Map(visible.map(([model], index) => [model, index]))
  const dailyValues = new Map<string, number[]>()
  if (view === "models") {
    for (const row of modelRows) {
      const values = dailyValues.get(row.date) || series.map(() => 0)
      const index = modelIndex.get(row.model) ?? series.length - 1
      values[index] += row.total_tokens
      dailyValues.set(row.date, values)
    }
  }
  const points = []
  for (let ms = first; ms <= last; ms += 86400000) {
    const id = new Date(ms).toISOString().slice(0, 10), row = daily.get(id)
    const input = row?.input_tokens || 0, output = row?.output_tokens || 0
    points.push({ id, input, output, missing: row?.missing_usage_requests || 0,
      values: view === "tokens" ? [input, output] : dailyValues.get(id) || series.map(() => 0) })
  }
  return { series, points }
}
