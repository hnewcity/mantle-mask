import type { UsageCost, UsageTotals } from "@/lib/api"

export function estimatedCost(cost: UsageCost | undefined, totals: UsageTotals): number | null {
  if (!cost) return null
  const unknown = cost.unpriced_requests + totals.missing_usage_requests + totals.in_flight_requests + (totals.pending_sync_requests || 0)
  return !cost.priced_requests && unknown > 0 ? null : cost.estimated_usd
}

export function formatUsageCost(cost: UsageCost | undefined, totals: UsageTotals): string {
  const value = estimatedCost(cost, totals)
  if (value === null) return "—"
  if (value > 0 && value < .000001) return "<$0.000001"
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 6 }).format(value)
}

export function formatTokenPrice(value: number | undefined): string {
  if (value === undefined) return "同输入"
  if (value > 0 && value < 1e-12) return value.toExponential(3)
  return Number(value.toPrecision(12)).toLocaleString("en-US", { maximumFractionDigits: 12 })
}
