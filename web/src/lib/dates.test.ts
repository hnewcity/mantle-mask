import { describe, expect, it } from "vitest"
import { formatLocalDate, parseLocalDate } from "./dates"

describe("date-only filters", () => {
  it("preserves local calendar dates across UTC offsets and daylight-saving boundaries", () => {
    const previous = process.env.TZ
    try {
      for (const zone of ["Asia/Shanghai", "America/Los_Angeles", "Pacific/Auckland"]) {
        process.env.TZ = zone
        for (const value of ["2024-02-29", "2026-03-08", "2026-11-01", "2026-12-31"]) {
          const date = parseLocalDate(value)
          expect(date.getHours()).toBe(0)
          expect(formatLocalDate(date)).toBe(value)
        }
      }
    } finally {
      if (previous === undefined) delete process.env.TZ
      else process.env.TZ = previous
    }
  })
})
