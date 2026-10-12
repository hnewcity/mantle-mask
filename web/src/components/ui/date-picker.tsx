import { lazy, Suspense, useState } from "react"
import * as Popover from "@radix-ui/react-popover"
import { zhCN } from "@daypicker/react/locale"
import { CalendarDays } from "lucide-react"
import { Button } from "@/components/ui/button"
import { formatLocalDate, parseLocalDate } from "@/lib/dates"

const Calendar = lazy(() => import("@daypicker/react").then(module => ({ default: module.DayPicker })))

type DatePickerProps = {
  value: string
  onValueChange: (value: string) => void
  "aria-label": string
  min?: string
  max?: string
  disabled?: boolean
}

export function DatePicker({ value, onValueChange, min, max, disabled, "aria-label": label }: DatePickerProps) {
  const [open, setOpen] = useState(false)
  const selected = parseLocalDate(value), earliest = min ? parseLocalDate(min) : undefined, latest = max ? parseLocalDate(max) : undefined
  const today = formatLocalDate(new Date())
  function choose(day: Date) {
    onValueChange(formatLocalDate(day))
    setOpen(false)
  }
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><button type="button" className="select-trigger date-trigger" aria-label={label} disabled={disabled}>
      <span>{value}</span><CalendarDays size={15} aria-hidden="true" />
    </button></Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className="date-popover" align="end" sideOffset={6} collisionPadding={12} aria-label={`选择${label}`}>
        <div className="calendar-label">{label}</div>
        <Suspense fallback={<div className="calendar-loading" role="status">加载日历…</div>}><Calendar className="calendar" mode="single" required selected={selected} defaultMonth={selected} onSelect={choose}
          locale={zhCN} weekStartsOn={1} fixedWeeks showOutsideDays autoFocus navLayout="around"
          startMonth={earliest} endMonth={latest} disabled={[...(earliest ? [{ before: earliest }] : []), ...(latest ? [{ after: latest }] : [])]}
          labels={{
            labelPrevious: () => "上个月", labelNext: () => "下个月",
            labelDayButton: (day, modifiers) => `${formatLocalDate(day)}${modifiers.today ? "，今天" : ""}${modifiers.selected ? "，已选择" : ""}`,
          }} /></Suspense>
        <div className="calendar-footer"><span>按本机时区</span><Button variant="ghost" size="sm" disabled={Boolean(min && today < min || max && today > max)} onClick={() => choose(parseLocalDate(today))}>今天</Button></div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}
