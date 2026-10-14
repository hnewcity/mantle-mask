import { expect, test } from "@playwright/test"
import type { PriceCatalog, Upstream } from "../src/lib/api"
const admin = { Authorization: "Bearer e2e-admin-token-only-for-local-tests" }
function payload(upstream: Upstream) {
  const { has_credentials: _credentials, ...input } = upstream
  return input
}

test("reference prices match upstream regions and aliases, preserve overrides, and retain cache on sync failure", async ({ page }) => {
  const errors: string[] = []
  page.on("pageerror", error => errors.push(error.message))
  const { data: previous }: { data: Upstream[] } = await (await page.request.get("/api/admin/upstreams", { headers: admin })).json()
  const manual = await (await page.request.get("/api/admin/usage/pricing", { headers: admin })).json()
  const catalog: PriceCatalog = await (await page.request.get("/api/admin/usage/pricing/reference", { headers: admin })).json()
  const created: string[] = []
  try {
    for (const upstream of previous.filter(u => u.enabled)) await page.request.put(`/api/admin/upstreams/${upstream.id}`, { headers: admin, data: { ...payload(upstream), enabled: false } })
    await page.request.put("/api/admin/usage/pricing", { headers: admin, data: {} })
    const user = await (await page.request.post("/api/admin/users", { headers: admin, data: { name: "参考费用测试团队" } })).json()
    const key = await (await page.request.post("/api/admin/keys", { headers: admin, data: { name: "参考价格测试 Key", user_id: user.id } })).json()
    for (const [alias, region] of [["reference-east", "us-east-1"], ["reference-west", "us-west-2"]]) {
      const result = await page.request.post("/api/admin/upstreams", { headers: admin, data: {
        name: `参考价格 ${region}`, base_url: "http://127.0.0.1:18090/v1", region, auth_mode: "api_key", api_key: "mock-bedrock-key", weight: 1, enabled: true, models: { [alias]: "e2e.reference-model" },
      } })
      expect(result.ok()).toBe(true)
      created.push((await result.json()).id)
      expect((await page.request.post("/v1/chat/completions", { headers: { Authorization: `Bearer ${key.token}` }, data: { model: alias, stream: false, messages: [{ role: "user", content: "reference fixture" }] } })).ok()).toBe(true)
    }
    await page.goto("/")
    await page.getByLabel("管理员令牌").fill("e2e-admin-token-only-for-local-tests")
    await page.getByRole("button", { name: "进入控制台" }).click()
    await expect(page.getByRole("heading", { name: "网关总览", exact: true })).toBeVisible()
    await page.goto(`/#/usage?user_id=${user.id}`)
    const cost = page.locator(".usage-cost-card .stat-value")
    await expect(cost).toHaveText("$0.000104")
    await page.getByRole("button", { name: "配置模型单价", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "模型单价", exact: true })
    const references = dialog.getByRole("region", { name: "参考价格", exact: true })
    await expect(references).toContainText("AWS 官方")
    await expect(references).toContainText("us-west-2")
    await references.getByLabel("搜索参考价格", { exact: true }).fill("us-east-1")
    await expect(references.locator(".usage-reference-row")).toHaveCount(1)
    await references.getByLabel("搜索参考价格", { exact: true }).fill("")
    // Sync UI is mocked; report calculations above use real seeded server data.
    await page.route("**/api/admin/usage/pricing/sync", route => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...catalog, updated_at: new Date().toISOString() }) }))
    await references.getByRole("button", { name: "同步参考价格", exact: true }).click()
    await expect(page.getByRole("status").filter({ hasText: "手动覆盖保持不变" })).toBeVisible()
    await dialog.getByRole("button", { name: "添加模型单价", exact: true }).click()
    await dialog.getByLabel("模型 1", { exact: true }).fill("reference-east")
    await dialog.getByRole("spinbutton", { name: "输入单价 1", exact: true }).fill("9")
    await dialog.getByRole("spinbutton", { name: "输出单价 1", exact: true }).fill("10")
    await dialog.getByRole("button", { name: "保存单价", exact: true }).click()
    await expect(cost).toHaveText("$0.000264")
    await page.getByRole("button", { name: "配置模型单价", exact: true }).click()
    await expect(dialog.getByLabel("模型 1", { exact: true })).toHaveValue("reference-east")
    await dialog.getByRole("button", { name: "同步参考价格", exact: true }).click()
    await expect(dialog.getByLabel("模型 1", { exact: true })).toHaveValue("reference-east")
    const afterSync = await (await page.request.get("/api/admin/usage/pricing", { headers: admin })).json()
    expect(afterSync["reference-east"].input_usd_per_million).toBe(9)
    await page.unroute("**/api/admin/usage/pricing/sync")
    await page.route("**/api/admin/usage/pricing/sync", route => route.fulfill({ status: 502, contentType: "application/json", body: JSON.stringify({ error: { message: "模拟价格源离线" } }) }))
    await dialog.getByRole("button", { name: "同步参考价格", exact: true }).click()
    await expect(dialog.getByRole("alert")).toContainText("原有参考价格和手动覆盖未被清空")
    await expect(references.locator(".usage-reference-row")).toHaveCount(catalog.entries.length)
    await page.unroute("**/api/admin/usage/pricing/sync")
    await page.route("**/api/admin/usage/pricing/sync", route => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...catalog, updated_at: new Date().toISOString() }) }))
    await dialog.getByRole("button", { name: "同步参考价格", exact: true }).click()
    await expect(dialog.getByRole("button", { name: "同步参考价格", exact: true })).toBeEnabled()
    await expect(dialog.getByRole("alert")).toHaveCount(0)
    if (await page.getByRole("button", { name: "关闭通知", exact: true }).isVisible()) await page.getByRole("button", { name: "关闭通知", exact: true }).click()
    // Radix makes the background toast inert while this dialog is open.
    await expect(page.locator(".toast")).toHaveCount(0, { timeout: 10000 })
    await page.screenshot({ path: "../.tools/price-sync-desktop.png" })
    await page.setViewportSize({ width: 390, height: 844 })
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
    await page.screenshot({ path: "../.tools/price-sync-mobile.png" })
    await dialog.getByRole("button", { name: "取消", exact: true }).click()
    await expect(cost).toHaveText("$0.000264")
    const userHeaders = { Authorization: `Bearer ${key.token}` }
    expect((await page.request.get("/api/admin/usage/pricing/reference", { headers: userHeaders })).status()).toBe(401)
    expect((await page.request.post("/api/admin/usage/pricing/sync", { headers: userHeaders, data: {} })).status()).toBe(401)
    expect(errors).toEqual([])
  } finally {
    await page.request.put("/api/admin/usage/pricing", { headers: admin, data: manual })
    for (const id of created) await page.request.delete(`/api/admin/upstreams/${id}`, { headers: admin })
    for (const upstream of previous.filter(u => u.enabled)) await page.request.put(`/api/admin/upstreams/${upstream.id}`, { headers: admin, data: payload(upstream) })
  }
})
