import { expect, test } from "@playwright/test"
import type { ModelPrices, Upstream, UsageReport } from "../src/lib/api"

const admin = { Authorization: "Bearer e2e-admin-token-only-for-local-tests" }
function payload(upstream: Upstream) {
  const { has_credentials: _credentials, ...input } = upstream
  return input
}

test("model prices, cost filtering, CSV, private scope and responsive layout", async ({ page }) => {
  const errors: string[] = []
  page.on("pageerror", error => errors.push(error.message))
  const { data: previous }: { data: Upstream[] } = await (await page.request.get("/api/admin/upstreams", { headers: admin })).json()
  const previousPrices: ModelPrices = await (await page.request.get("/api/admin/usage/pricing", { headers: admin })).json()
  let upstreamID = ""
  try {
    for (const upstream of previous.filter(u => u.enabled)) await page.request.put(`/api/admin/upstreams/${upstream.id}`, { headers: admin, data: { ...payload(upstream), enabled: false } })
    await page.request.put("/api/admin/usage/pricing", { headers: admin, data: {} })
    const user = await (await page.request.post("/api/admin/users", { headers: admin, data: { name: "费用测试团队" } })).json()
    const key = await (await page.request.post("/api/admin/keys", { headers: admin, data: { name: "费用测试 Key", user_id: user.id } })).json()
    const created = await page.request.post("/api/admin/upstreams", { headers: admin, data: {
      name: "费用测试上游", base_url: "http://127.0.0.1:18090/v1", region: "us-east-1", auth_mode: "api_key", api_key: "mock-bedrock-key", weight: 1, enabled: true, models: { "cost-alpha": "mock-model", "cost-beta": "mock-model" },
    } })
    expect(created.ok()).toBe(true)
    upstreamID = (await created.json()).id
    for (const [model, stream] of [["cost-alpha", false], ["cost-alpha", true], ["cost-beta", false]] as const) {
      expect((await page.request.post("/v1/chat/completions", { headers: { Authorization: `Bearer ${key.token}` }, data: { model, stream, messages: [{ role: "user", content: "test cost" }] } })).ok()).toBe(true)
    }
    await page.goto("/")
    await page.getByLabel("管理员令牌").fill("e2e-admin-token-only-for-local-tests")
    await page.getByRole("button", { name: "进入控制台" }).click()
    await expect(page.getByRole("heading", { name: "网关总览", exact: true })).toBeVisible()
    await page.goto(`/#/usage?user_id=${user.id}`)
    const cost = page.locator(".usage-cost-card")
    await expect(cost.locator(".stat-value")).toHaveText("—")
    await expect(page.getByText("3 次请求未匹配模型单价", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "配置模型单价", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "模型单价", exact: true })
    await expect(dialog.getByText("尚未配置单价。", { exact: false })).toBeVisible()
    for (const [index, model, input, output] of [[1, "cost-alpha", "1", "2"], [2, "cost-beta", "3", "4"]] as const) {
      await dialog.getByRole("button", { name: "添加模型单价", exact: true }).click()
      await dialog.getByLabel(`模型 ${index}`, { exact: true }).fill(model)
      await dialog.getByRole("spinbutton", { name: `输入单价 ${index}`, exact: true }).fill(input)
      await dialog.getByRole("spinbutton", { name: `输出单价 ${index}`, exact: true }).fill(output)
    }
    await dialog.getByRole("spinbutton", { name: "缓存输入单价 1", exact: true }).fill("0")
    await dialog.getByRole("button", { name: "保存单价", exact: true }).click()
    await expect(dialog).not.toBeVisible()
    await expect(cost.locator(".stat-value")).toHaveText("$0.000136")
    await expect(cost).toContainText("3 次已估算 · 0 次未匹配单价")
    const breakdown = page.locator(".usage-breakdown")
    await expect(breakdown.getByRole("row").filter({ hasText: "cost-alpha" })).toContainText("$0.000064")
    await expect(breakdown.getByRole("row").filter({ hasText: "cost-beta" })).toContainText("$0.000072")
    for (const group of ["按用户", "按密钥", "按上游", "按日期"]) {
      await page.getByRole("tab", { name: group, exact: true }).click()
      await expect(breakdown.locator("tbody tr")).toContainText("$0.000136")
    }
    const downloadEvent = page.waitForEvent("download")
    await page.getByRole("button", { name: "导出 CSV", exact: true }).click()
    const stream = await (await downloadEvent).createReadStream()
    const chunks: Buffer[] = []
    for await (const chunk of stream!) chunks.push(Buffer.from(chunk))
    const csv = Buffer.concat(chunks).toString("utf8")
    expect(csv).toContain("预估费用 (USD)")
    const costCell = csv.split("\r\n")[1].split(",")[11]
    expect(Number(costCell.replaceAll('"', ""))).toBeCloseTo(.000136, 12)
    await page.getByRole("button", { name: "筛选模型 cost-alpha", exact: true }).click()
    await expect(cost.locator(".stat-value")).toHaveText("$0.000064")
    await page.getByRole("button", { name: "清除模型筛选", exact: true }).click()
    await expect(cost.locator(".stat-value")).toHaveText("$0.000136")
    if (await page.getByRole("button", { name: "关闭通知", exact: true }).isVisible()) await page.getByRole("button", { name: "关闭通知", exact: true }).click()
    await page.screenshot({ path: "../.tools/usage-cost-desktop.png", fullPage: true })
    await page.getByRole("button", { name: "配置模型单价", exact: true }).click()
    await expect(dialog.getByRole("spinbutton", { name: "缓存输入单价 1", exact: true })).toHaveValue("0")
    // Duplicate model IDs must not silently overwrite each other.
    await dialog.getByLabel("模型 2", { exact: true }).fill("cost-alpha")
    await dialog.getByRole("button", { name: "保存单价", exact: true }).click()
    await expect(dialog.getByRole("alert")).toContainText("每个模型只能配置一组单价")
    await dialog.getByRole("button", { name: "删除模型单价 2", exact: true }).click()
    await dialog.getByRole("button", { name: "保存单价", exact: true }).click()
    await expect(cost.locator(".stat-value")).toHaveText("$0.000064")
    await expect(page.getByText("1 次请求未匹配模型单价", { exact: true })).toBeVisible()
    if (await page.getByRole("button", { name: "关闭通知", exact: true }).isVisible()) await page.getByRole("button", { name: "关闭通知", exact: true }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.getByRole("button", { name: "配置模型单价", exact: true }).click()
    await expect(dialog).toBeVisible()
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
    await page.screenshot({ path: "../.tools/usage-pricing-mobile.png" })
    await dialog.getByRole("button", { name: "取消", exact: true }).click()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
    const headers = { Authorization: `Bearer ${key.token}` }
    expect((await page.request.put("/api/admin/usage/pricing", { headers, data: {} })).status()).toBe(401)
    const privateReport: UsageReport = await (await page.request.get("/api/me/usage?all=true", { headers })).json()
    expect(privateReport.cost?.estimated_usd).toBeCloseTo(.000064, 12)
    expect(privateReport.cost?.unpriced_requests).toBe(1)
    await page.evaluate(token => sessionStorage.setItem("mantle-admin", token), key.token)
    await page.reload()
    await expect(page.getByRole("heading", { name: "我的用量", exact: true })).toBeVisible()
    await expect(cost.locator(".stat-value")).toHaveText("$0.000064")
    await expect(page.getByRole("button", { name: "配置模型单价", exact: true })).toHaveCount(0)
    await expect(page.getByText("请联系管理员同步参考价或配置模型单价。", { exact: false })).toBeVisible()
    expect(errors).toEqual([])
  } finally {
    if (upstreamID) await page.request.delete(`/api/admin/upstreams/${upstreamID}`, { headers: admin })
    await page.request.put("/api/admin/usage/pricing", { headers: admin, data: previousPrices })
    for (const upstream of previous.filter(u => u.enabled)) await page.request.put(`/api/admin/upstreams/${upstream.id}`, { headers: admin, data: payload(upstream) })
  }
})
