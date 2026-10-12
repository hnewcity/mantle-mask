import { describe, expect, it } from "vitest"
import type { UsageGroup, UsageReport, UsageTotals } from "./api"
import { buildUsageChart } from "./usage-chart"

const zero: UsageTotals = { requests: 0, errors: 0, input_tokens: 0, output_tokens: 0, total_tokens: 0, cached_tokens: 0, reasoning_tokens: 0, known_usage_requests: 0, missing_usage_requests: 0, in_flight_requests: 0 }
const daily = (id: string, input: number, output: number): UsageGroup => ({ ...zero, id, name: id, input_tokens: input, output_tokens: output, total_tokens: input + output })
function fixture(overrides: Partial<UsageReport> = {}): UsageReport {
  return { from: "2026-10-01", to: "2026-10-03", all: false, timezone_offset: 480, totals: zero, by_user: [], by_key: [], by_model: [],
    daily: [daily("2026-10-01", 100, 20), daily("2026-10-03", 10, 5)],
    daily_by_model: [
      { ...zero, date: "2026-10-01", model: "a", input_tokens: 70, output_tokens: 10, total_tokens: 80 },
      { ...zero, date: "2026-10-01", model: "b", input_tokens: 30, output_tokens: 10, total_tokens: 40 },
      { ...zero, date: "2026-10-03", model: "b", input_tokens: 10, output_tokens: 5, total_tokens: 15 },
    ], ...overrides }
}

describe("daily usage chart", () => {
  it("uses the console theme for bars and legends in both views", () => {
    expect(buildUsageChart(fixture(), "tokens").series.map(s => s.color)).toEqual(["var(--chart-input)", "var(--chart-output)"])
    const rows = Array.from({ length: 8 }, (_, index) => ({ ...zero, date: "2026-10-01", model: `m${index}`, total_tokens: 10 - index }))
    for (const daily_by_model of [rows.slice(0, 6), rows]) {
      const colors = buildUsageChart(fixture({ daily_by_model }), "models").series.map(s => s.color)
      expect(colors).toEqual(Array.from({ length: 6 }, (_, index) => `var(--chart-model-${index + 1})`))
      expect(new Set(colors).size).toBe(6)
    }
  })
  it("preserves input/output totals and fills calendar gaps", () => {
    const { points, series } = buildUsageChart(fixture(), "tokens")
    expect(series.map(s => s.label)).toEqual(["输入", "输出"])
    expect(points.map(p => [p.id, p.values])).toEqual([["2026-10-01", [100, 20]], ["2026-10-02", [0, 0]], ["2026-10-03", [10, 5]]])
  })
  it("compares actual daily model totals, never prorates aggregate shares", () => {
    const { points, series } = buildUsageChart(fixture(), "models")
    expect(series.map(s => s.model)).toEqual(["a", "b"])
    expect(points.map(p => p.values)).toEqual([[80, 40], [0, 0], [0, 15]])
    for (const point of points) expect(point.values.reduce((sum, value) => sum + value, 0)).toBe(point.input + point.output)
  })
  it("shows all six models or groups the tail without dropping tokens", () => {
    const rows = Array.from({ length: 8 }, (_, index) => ({ ...zero, date: "2026-10-01", model: index ? `m${index}` : "", total_tokens: 10 - index }))
    const { series, points } = buildUsageChart(fixture({ daily_by_model: rows }), "models")
    expect(series).toHaveLength(6)
    expect(series[0].label).toBe("未指定模型")
    expect(series.at(-1)?.label).toBe("其他 3 个模型")
    expect(points[0].values).toEqual([10, 9, 8, 7, 6, 12])
    expect(buildUsageChart(fixture({ daily_by_model: rows.slice(0, 6) }), "models").series.map(s => s.id)).not.toContain("other")
  })
  it("bounds either a custom range or all history to the last 90 calendar days", () => {
    for (const all of [true, false]) {
      const report = fixture({ all, from: "2026-06-01", to: "2026-10-03", daily: [daily("2026-10-03", 10, 5), daily("2026-06-01", 100, 0)],
        daily_by_model: [{ ...zero, date: "2026-06-01", model: "old", total_tokens: 100 }, { ...zero, date: "2026-10-03", model: "recent", total_tokens: 15 }] })
      const { points, series } = buildUsageChart(report, "models")
      expect(points).toHaveLength(90)
      expect(points[0].id).toBe("2026-07-06")
      expect(points.at(-1)?.id).toBe("2026-10-03")
      expect(series.map(series => series.model)).toEqual(["recent"])
      expect(points.at(-1)?.values).toEqual([15])
      expect(report.daily).toHaveLength(2)
    }
  })
  it("keeps missing usage counts and supports empty and one-day ranges", () => {
    expect(buildUsageChart(fixture({ daily: [], daily_by_model: [] }), "models").points).toEqual([])
    const { points } = buildUsageChart(fixture({ from: "2026-10-01", to: "2026-10-01", daily: [{ ...daily("2026-10-01", 0, 0), missing_usage_requests: 2 }] }), "tokens")
    expect(points).toHaveLength(1)
    expect(points[0].missing).toBe(2)
    expect(points[0].values).toEqual([0, 0])
  })
})
