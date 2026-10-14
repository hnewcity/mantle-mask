import { describe, expect, it } from "vitest"
import type { UsageCost, UsageTotals } from "@/lib/api"
import { estimatedCost, formatUsageCost, formatTokenPrice } from "@/lib/usage-cost"

const totals: UsageTotals = { requests: 1, errors: 0, input_tokens: 0, output_tokens: 0, total_tokens: 0, cached_tokens: 0, reasoning_tokens: 0, known_usage_requests: 1, missing_usage_requests: 0, in_flight_requests: 0 }
const cost: UsageCost = { estimated_usd: .000032, priced_requests: 1, unpriced_requests: 0 }

describe("usage costs", () => {
  it("keeps tiny real charges visible", () => {
    expect(formatUsageCost(cost, totals)).toBe("$0.000032")
    expect(formatUsageCost({ ...cost, estimated_usd: .00000001 }, totals)).toBe("<$0.000001")
    expect(formatUsageCost({ ...cost, estimated_usd: 12.3456789 }, totals)).toBe("$12.345679")
  })
  it("distinguishes free requests and empty ranges from unpriced or missing usage", () => {
    expect(formatUsageCost({ ...cost, estimated_usd: 0 }, totals)).toBe("$0.00")
    const unpriced = { ...cost, estimated_usd: 0, priced_requests: 0, unpriced_requests: 1 }
    expect(formatUsageCost(unpriced, totals)).toBe("—")
    expect(estimatedCost(unpriced, totals)).toBeNull()
    const empty = { ...unpriced, unpriced_requests: 0 }
    expect(formatUsageCost(empty, { ...totals, requests: 0 })).toBe("$0.00")
    for (const partial of [{ missing_usage_requests: 1 }, { in_flight_requests: 1 }, { pending_sync_requests: 1 }]) {
      expect(formatUsageCost(empty, { ...totals, ...partial })).toBe("—")
    }
    expect(formatUsageCost(undefined, totals)).toBe("—")
  })
  it("exports the unrounded estimate and shows priced portions of incomplete ranges", () => {
    expect(estimatedCost(cost, totals)).toBe(.000032)
    expect(formatUsageCost({ ...cost, unpriced_requests: 3 }, totals)).toBe("$0.000032")
  })
})

it("formats converted reference rates without float tails or false zero prices", () => {
  expect(formatTokenPrice(.06999999999999999)).toBe("0.07")
  expect(formatTokenPrice(0)).toBe("0")
  expect(formatTokenPrice(undefined)).toBe("同输入")
  expect(formatTokenPrice(1e-15)).toBe("1.000e-15")
})
