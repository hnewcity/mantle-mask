import { expect, test } from "@playwright/test"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import type { APIKey, Metrics, Settings, Upstream, UsageTotals, User } from "../src/lib/api"

test.use({ locale: "zh-CN", timezoneId: "Asia/Shanghai" })

// Documentation-only fixtures. This renders the real bundled console, but never
// reads production data, calls AWS, or displays working credentials.
test("README overview screenshot with isolated demonstration data", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1120 })
  await page.emulateMedia({ reducedMotion: "reduce" })
  const errors: string[] = []
  page.on("pageerror", error => errors.push(error.message))
  const now = "2026-10-12T10:00:00Z"
  const upstreams: Upstream[] = [
    { id: "demo-west", name: "US West · Primary", region: "us-west-2", weight: 3 },
    { id: "demo-east", name: "US East · Fallback", region: "us-east-1", weight: 1 },
    { id: "demo-lab", name: "Development · Sandbox", region: "us-east-1", weight: 1 },
  ].map((upstream, index) => ({
    ...upstream, base_url: "https://bedrock-mantle." + upstream.region + ".api.aws/v1",
    auth_mode: "api_key", signing_service: "bedrock-mantle", enabled: index !== 2,
    models: { "gpt-oss": "openai.gpt-oss-120b" }, has_credentials: true, created_at: now,
  }))
  const users: User[] = [{ id: "demo-user", name: "Demo workspace", note: "Synthetic screenshot fixture", enabled: true, created_at: now }]
  const keys: APIKey[] = [{ id: "demo-key", name: "Playground", prefix: "demo-only", user_id: "demo-user", created_at: now }]
  const traffic = [6, 9, 7, 14, 11, 8, 17, 21, 14, 18, 12, 24, 28, 21, 17, 25, 19, 31, 26, 35, 22, 27, 33, 29, 40, 32, 38, 28, 34, 42]
  const metrics: Metrics = {
    started_at: "2026-10-12T00:00:00Z", requests: 1284, errors: 3,
    tokens: 486320, avg_latency_ms: 842, usage_storage_healthy: true,
    timeline: traffic.map((requests, i) => ({ time: new Date(Date.parse(now) - (29 - i) * 60000).toISOString(), requests, errors: 0 })),
    health: Object.fromEntries(upstreams.slice(0, 2).map(u => [u.id, { status: "healthy", checked_at: now, cooldown_until: "", latency_ms: 146 }])),
    recent: ["openai.gpt-oss-120b", "openai.gpt-oss-20b", "openai.gpt-oss-120b"].map((model, i) => ({
      id: "demo-request-" + i, time: new Date(Date.parse(now) - i * 15000).toISOString(),
      method: "POST", path: i === 1 ? "/v1/responses" : "/v1/chat/completions",
      model, upstream: upstreams[i % 2].id, upstream_name: upstreams[i % 2].name,
      key_name: "Playground", status: 200, duration_ms: [786, 1240, 652][i], stream: true,
      attempts: 1, input_tokens: 128, output_tokens: 256, user_id: "demo-user",
      user_name: "Demo workspace", key_id: "demo-key", usage_known: true, inference: true,
    })),
  }
  const totals: UsageTotals = { requests: 1284, errors: 3, input_tokens: 186320, output_tokens: 300000, total_tokens: 486320, cached_tokens: 0, reasoning_tokens: 0, known_usage_requests: 1281, missing_usage_requests: 3, in_flight_requests: 0 }
  const settings: Settings = { request_timeout_seconds: 300, max_retries: 2, cooldown_seconds: 30, log_capacity: 1000, allow_http_upstreams: false }
  const fixtures: Record<string, unknown> = {
    "/api/session": { role: "admin" },
    "/api/admin/upstreams": { data: upstreams },
    "/api/admin/keys": { data: keys },
    "/api/admin/users": { data: users },
    "/api/admin/overview": metrics,
    "/api/admin/settings": settings,
    "/api/admin/usage": { from: "2026-10-12", to: "2026-10-12", all: true, timezone_offset: -480, totals, by_user: [], by_key: [], by_model: [], by_upstream: [], daily: [] },
  }
  await page.route("**/api/**", async route => {
    const endpoint = new URL(route.request().url()).pathname
    if (!(endpoint in fixtures)) {
      errors.push("Unexpected screenshot API: " + endpoint)
      await route.abort()
      return
    }
    await route.fulfill({ json: fixtures[endpoint] })
  })
  await page.addInitScript(() => sessionStorage.setItem("mantle-admin", "documentation-fixture-not-a-real-token"))
  await page.goto("/#/overview")
  await expect(page.getByRole("heading", { name: "网关总览", exact: true })).toBeVisible()
  await expect(page.getByText("1,284", { exact: true })).toBeVisible()
  await expect(page.getByText("连接正常", { exact: true })).toHaveCount(2)
  await page.evaluate(() => document.fonts.ready)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  expect(errors).toEqual([])
  const output = process.env.UPDATE_SCREENSHOTS === "1" ? "../docs/images/overview.png" : "../test-results/readme-overview.png"
  await mkdir(path.dirname(output), { recursive: true })
  await page.screenshot({ path: output, fullPage: true, animations: "disabled" })
})
