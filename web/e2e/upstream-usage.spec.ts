import { expect, test } from "@playwright/test"
import type { Upstream } from "../src/lib/api"

function payload(upstream: Upstream) {
  const { has_credentials: _credentials, ...input } = upstream
  return input
}

const admin = { Authorization: "Bearer e2e-admin-token-only-for-local-tests" }

test("upstream usage grouping, combined filters, deleted history, CSV and mobile layout", async ({ page }) => {
  const errors: string[] = []
  page.on("pageerror", error => errors.push(error.message))
  // Other specs share the mock gateway; isolate routing without deleting their fixtures.
  const { data: previous }: { data: Upstream[] } = await (await page.request.get("/api/admin/upstreams", { headers: admin })).json()
  const created: string[] = []
  try {
    for (const upstream of previous.filter(u => u.enabled)) {
      expect((await page.request.put(`/api/admin/upstreams/${upstream.id}`, { headers: admin, data: { ...payload(upstream), enabled: false } })).ok()).toBe(true)
    }
    const user = await (await page.request.post("/api/admin/users", { headers: admin, data: { name: "上游统计测试团队" } })).json()
    const key = await (await page.request.post("/api/admin/keys", { headers: admin, data: { name: "上游统计测试 Key", user_id: user.id } })).json()
    const definitions = [
      { name: "统计上游 A", models: { "usage-alpha": "mock-model" } },
      { name: "统计上游 B", models: { "usage-beta": "mock-model" } },
      { name: "尚未调用的上游", models: { "usage-unused": "mock-model" } },
    ]
    for (const definition of definitions) {
      const response = await page.request.post("/api/admin/upstreams", { headers: admin, data: {
        ...definition, base_url: "http://127.0.0.1:18090/v1", region: "us-east-1", auth_mode: "api_key", api_key: "mock-bedrock-key", weight: 1, enabled: true,
      } })
      expect(response.status()).toBe(200)
      created.push((await response.json()).id)
    }
    for (const [model, stream] of [["usage-alpha", false], ["usage-alpha", true], ["usage-beta", false]] as const) {
      const response = await page.request.post("/v1/chat/completions", { headers: { Authorization: `Bearer ${key.token}` }, data: { model, stream, messages: [{ role: "user", content: "test usage" }] } })
      expect(response.ok()).toBe(true)
    }
    expect((await page.request.delete(`/api/admin/upstreams/${created[1]}`, { headers: admin })).ok()).toBe(true)
    const { data: current }: { data: Upstream[] } = await (await page.request.get("/api/admin/upstreams", { headers: admin })).json()
    const a = current.find(u => u.id === created[0])!
    expect((await page.request.put(`/api/admin/upstreams/${a.id}`, { headers: admin, data: { ...payload(a), name: "统计上游 A（新名称）", enabled: false } })).ok()).toBe(true)

    await page.goto("/")
    await page.getByLabel("管理员令牌").fill("e2e-admin-token-only-for-local-tests")
    await page.getByRole("button", { name: "进入控制台" }).click()
    await expect(page.getByRole("heading", { name: "网关总览", exact: true })).toBeVisible()
    await page.goto(`/#/usage?user_id=${user.id}`)
    const total = page.locator(".usage-stats .stat-value").first()
    const breakdown = page.locator(".usage-breakdown")
    const upstreamSelect = page.getByRole("combobox", { name: "筛选上游", exact: true })
    await expect(total).toHaveText("60")
    const chart = page.getByRole("region", { name: "每日 Token 用量", exact: true })
    const globalFilters = page.getByRole("region", { name: "用量筛选", exact: true })
    await chart.getByRole("button", { name: "按模型", exact: true }).click()
    await expect(chart.getByRole("button", { name: "按模型", exact: true })).toHaveAttribute("aria-pressed", "true")
    await expect(chart.locator("svg title").last()).toContainText("usage-alpha 40")
    await expect(chart.locator("svg title").last()).toContainText("usage-beta 20")
    // Resolve theme variables in the browser: bars and legend swatches must match,
    // and each model uses a distinct neutral shade rather than an unrelated hue.
    const palette = await chart.evaluate(element => {
      const canvas = document.createElement("canvas")
      canvas.width = canvas.height = 1
      const context = canvas.getContext("2d")!
      const pixel = (color: string) => {
        context.clearRect(0, 0, 1, 1)
        context.fillStyle = color
        context.fillRect(0, 0, 1, 1)
        return [...context.getImageData(0, 0, 1, 1).data]
      }
      return {
        bars: [...element.querySelectorAll("svg g:last-child rect")].slice(1).map(rect => pixel(getComputedStyle(rect).fill)),
        legend: [...element.querySelectorAll(".usage-legend i")].map(swatch => pixel(getComputedStyle(swatch).backgroundColor)),
        foreground: pixel(getComputedStyle(element).getPropertyValue("--foreground")),
      }
    })
    expect(palette.bars).toEqual(palette.legend)
    expect(palette.bars[0]).toEqual(palette.foreground)
    expect(new Set(palette.bars.map(color => color.join(","))).size).toBe(2)
    for (const [red, green, blue, alpha] of palette.bars) {
      expect(red).toBe(green)
      expect(green).toBe(blue)
      expect(alpha).toBe(255)
    }
    await chart.getByRole("button", { name: "usage-beta", exact: true }).click()
    await expect(total).toHaveText("20")
    await expect(globalFilters.getByRole("combobox", { name: "筛选模型", exact: true })).toHaveText("usage-beta")
    await expect(chart.locator("svg title").last()).not.toContainText("usage-alpha")
    await chart.getByRole("combobox", { name: "图表筛选模型", exact: true }).click()
    await page.getByRole("option", { name: "全部模型", exact: true }).click()
    await expect(total).toHaveText("60")
    for (const [label, count] of [["近 90 天", 90], ["近 7 天", 7], ["今天", 1]] as const) {
      await chart.getByRole("button", { name: label, exact: true }).click()
      await expect(chart.locator("svg title")).toHaveCount(count)
      await expect(globalFilters.getByRole("button", { name: label, exact: true })).toHaveAttribute("aria-pressed", "true")
      await expect(chart.getByRole("button", { name: label, exact: true })).toHaveAttribute("aria-pressed", "true")
      await expect(total).toHaveText("60")
    }
    await chart.getByRole("button", { name: "全部历史", exact: true }).click()
    await expect(chart.getByRole("button", { name: "图表开始日期", exact: true })).toBeDisabled()
    await expect(globalFilters.getByRole("button", { name: "全部历史", exact: true })).toHaveAttribute("aria-pressed", "true")
    await chart.getByRole("button", { name: "自定义日期", exact: true }).click()
    await chart.getByRole("button", { name: "近 7 天", exact: true }).click()
    const chartStart = chart.getByRole("button", { name: "图表开始日期", exact: true })
    await chartStart.click()
    await page.getByRole("dialog", { name: "选择图表开始日期", exact: true }).getByRole("button", { name: "今天", exact: true }).click()
    await expect(chart.locator("svg title")).toHaveCount(1)
    await expect(chartStart).toHaveText(await globalFilters.getByRole("button", { name: "开始日期", exact: true }).innerText())
    await chart.locator("svg g").last().focus()
    await expect(chart.locator(".usage-chart-detail")).toContainText("usage-alpha 40")
    await chart.getByRole("button", { name: "输入 / 输出", exact: true }).click()
    await expect(chart.getByRole("img")).toHaveAttribute("aria-label", "每日输入和输出 Token 用量，最多展示最近 90 天")
    await chart.getByRole("button", { name: "近 30 天", exact: true }).click()
    await expect(chart.locator("svg title")).toHaveCount(30)
    await page.getByRole("tab", { name: "按上游", exact: true }).click()
    await expect(breakdown.locator("tbody tr")).toHaveCount(2)
    await expect(breakdown.getByRole("row").filter({ hasText: "统计上游 A（新名称）（已停用）" })).toContainText("40")
    await expect(breakdown.getByRole("row").filter({ hasText: "统计上游 B（已删除）" })).toContainText("20")

    await upstreamSelect.click()
    await page.getByRole("option", { name: "统计上游 B（已删除）", exact: true }).click()
    await expect(total).toHaveText("20")
    await expect(breakdown.locator("tbody tr")).toHaveCount(1)
    const models = page.getByRole("region", { name: "模型用量", exact: true })
    await expect(models.getByRole("button", { name: "筛选模型 usage-beta", exact: true })).toContainText("100.0%")
    await expect(models.getByRole("button", { name: "筛选模型 usage-alpha", exact: true })).toHaveCount(0)
    await models.getByRole("button", { name: "筛选模型 usage-beta", exact: true }).click()
    await upstreamSelect.click()
    await expect(page.getByRole("option", { name: "统计上游 A（新名称）（已停用）", exact: true })).toBeVisible()
    await page.getByRole("option", { name: "统计上游 A（新名称）（已停用）", exact: true }).click()
    await expect(total).toHaveText("0")
    await expect(breakdown.getByText("暂无用量明细", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "清除模型筛选", exact: true }).click()
    await expect(total).toHaveText("40")
    await page.getByRole("tab", { name: "按日期", exact: true }).click()
    await expect(breakdown.locator("tbody tr")).toHaveCount(1)
    await expect(breakdown.locator("tbody tr")).toContainText("40")
    await expect(page.locator(".usage-chart")).toContainText("40")
    await page.getByRole("tab", { name: "按上游", exact: true }).click()
    await page.getByRole("button", { name: "清除上游筛选", exact: true }).click()
    await expect(total).toHaveText("60")
    await expect(breakdown.locator("tbody tr")).toHaveCount(2)
    const downloadEvent = page.waitForEvent("download")
    await page.getByRole("button", { name: "导出 CSV", exact: true }).click()
    const download = await downloadEvent
    expect(download.suggestedFilename()).toMatch(/mantle-usage-by_upstream.*\.csv/)
    const stream = await download.createReadStream()
    const chunks: Buffer[] = []
    for await (const chunk of stream!) chunks.push(Buffer.from(chunk))
    const csv = Buffer.concat(chunks).toString("utf8")
    expect(csv).toContain("统计上游 A（新名称）（已停用）")
    expect(csv).toContain("统计上游 B（已删除）")
    expect(csv).toContain(created[0])
    expect(csv).toContain(created[1])
    await chart.getByRole("button", { name: "按模型", exact: true }).click()
    await page.screenshot({ path: "../test-results/usage-upstreams-desktop.png", fullPage: true })
    await page.setViewportSize({ width: 1018, height: 888 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await chart.screenshot({ path: "../test-results/usage-daily-models.png" })
    for (const width of [390, 360]) {
      await page.setViewportSize({ width, height: 1000 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await expect(upstreamSelect).toBeVisible()
      await expect(chart.getByRole("combobox", { name: "图表筛选模型", exact: true })).toBeVisible()
      await chart.getByRole("button", { name: "近 7 天", exact: true }).click()
      await expect(chart.locator("svg title")).toHaveCount(7)
    }
    await page.screenshot({ path: "../test-results/usage-upstreams-mobile.png", fullPage: true })
    await upstreamSelect.click()
    await page.getByRole("option", { name: "尚未调用的上游", exact: true }).click()
    await expect(total).toHaveText("0")
    await expect(breakdown.getByText("暂无用量明细", { exact: true })).toBeVisible()
    await expect(chart.getByText("所选范围内暂无调用", { exact: true })).toBeVisible()
    // Empty ranges must keep their controls available to recover.
    await expect(chart.getByRole("button", { name: "近 90 天", exact: true })).toBeEnabled()
    expect(errors).toEqual([])
  } finally {
    for (const id of created) await page.request.delete(`/api/admin/upstreams/${id}`, { headers: admin })
    for (const upstream of previous.filter(u => u.enabled)) {
      await page.request.put(`/api/admin/upstreams/${upstream.id}`, { headers: admin, data: payload(upstream) })
    }
  }
})
