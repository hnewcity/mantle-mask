import { Button } from "@/components/ui/button"
import { DatePicker } from "@/components/ui/date-picker"
import { localDate } from "@/lib/dates"

export type UsagePeriodValue = { from: string; to: string; all: boolean }

export function UsagePeriod({ value, onChange, compact = false }: {
  value: UsagePeriodValue
  onChange: (value: UsagePeriodValue) => void
  compact?: boolean
}) {
  const { from, to, all } = value
  const prefix = compact ? "图表" : ""
  return <div className={compact ? "usage-chart-period" : "usage-period"}>
    <div className="period-shortcuts" role="group" aria-label={`${prefix}快捷日期`}>
      {([ ["今天", 0], ["近 7 天", 6], ["近 30 天", 29], ["近 90 天", 89] ] as const).map(([label, days]) => {
        const active = !all && from === localDate(days) && to === localDate()
        return <Button type="button" variant={active ? "secondary" : "ghost"} aria-pressed={active} size="sm" key={label}
          onClick={() => onChange({ all: false, from: localDate(days), to: localDate() })}>{label}</Button>
      })}
      <Button type="button" variant={all ? "secondary" : "ghost"} aria-pressed={all} size="sm" onClick={() => onChange({ ...value, all: true })}>全部历史</Button>
    </div>
    <div className="date-range">
      <DatePicker aria-label={`${prefix}开始日期`} value={from} disabled={all} max={to} onValueChange={from => onChange({ ...value, from })} />
      <span>至</span>
      <DatePicker aria-label={`${prefix}结束日期`} value={to} disabled={all} min={from} onValueChange={to => onChange({ ...value, to })} />
      {all && <Button type="button" variant="ghost" size="sm" onClick={() => onChange({ ...value, all: false })}>自定义日期</Button>}
    </div>
  </div>
}
