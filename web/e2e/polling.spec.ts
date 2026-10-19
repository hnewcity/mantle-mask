import { expect, test } from "@playwright/test"

test("console only polls full history on users page and pauses hidden tabs", async ({ page }) => {
  await page.addInitScript(() => sessionStorage.setItem("mantle-admin", "e2e-admin-token-only-for-local-tests"))
  await page.clock.install()
  let histories = 0, overviews = 0, reports = 0
  page.on("request", request => {
    const url = new URL(request.url())
    if (url.pathname === "/api/admin/usage") {
      reports++
      if (url.searchParams.get("all") === "true") histories++
    }
    if (url.pathname === "/api/admin/overview") overviews++
  })
  await page.goto("/#/overview")
  await expect(page.getByText("网关运行中", { exact: true })).toBeVisible()
  expect(histories).toBe(0)
  const initial = overviews
  await page.clock.fastForward(16000)
  await expect.poll(() => overviews).toBeGreaterThan(initial)
  expect(reports).toBe(0)

  await page.getByRole("link", { name: "用量统计", exact: true }).click()
  await expect(page.getByRole("heading", { name: "用量统计", exact: true })).toBeVisible()
  await expect.poll(() => reports).toBeGreaterThan(0)
  expect(histories).toBe(0)

  const loaded = page.waitForResponse(response => response.url().includes("/api/admin/usage?all=true"))
  await page.getByRole("link", { name: "用户管理", exact: true }).click()
  await loaded
  expect(histories).toBe(1)
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: true })
    document.dispatchEvent(new Event("visibilitychange"))
  })
  const pausedOverviews = overviews
  await page.clock.fastForward(120000)
  expect(histories).toBe(1)
  expect(overviews).toBe(pausedOverviews)
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: false })
    document.dispatchEvent(new Event("visibilitychange"))
  })
  await expect.poll(() => histories).toBe(2)
  const resumedOverviews = overviews
  await page.getByRole("link", { name: "总览", exact: true }).click()
  // Hash navigation and React effect cleanup are asynchronous. Do not advance
  // the fake clock until the new page's poller has replaced the users poller.
  await expect(page.getByRole("heading", { name: "网关总览", exact: true })).toBeVisible()
  await expect.poll(() => overviews).toBeGreaterThan(resumedOverviews)
  await page.clock.fastForward(16000)
  expect(histories).toBe(2)
})

test("logs bound rendered rows without truncating filtering or export", async ({ page }) => {
  await page.addInitScript(() => sessionStorage.setItem("mantle-admin", "e2e-admin-token-only-for-local-tests"))
  await page.route("**/api/admin/logs", route => route.fulfill({ json: { data: Array.from({ length: 225 }, (_, i) => ({
    id: `req-${i}`, time: "2026-10-19T12:00:00Z", path: "/v1/chat/completions", model: `model-${i}`,
    upstream_name: "mock", status: 200, duration_ms: 10, inference: true, usage_known: true,
    input_tokens: 2, output_tokens: 3, attempts: 1, user_name: "user", key_name: "key",
  })) } }))
  await page.goto("/#/logs")
  await expect(page.locator("tbody tr")).toHaveCount(100)
  await expect(page.getByRole("button", { name: "上一页", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "下一页", exact: true }).click()
  await expect(page.locator("tbody tr").first()).toContainText("model-100")
  await page.getByRole("button", { name: "下一页", exact: true }).click()
  await expect(page.locator("tbody tr")).toHaveCount(25)
  await expect(page.getByRole("button", { name: "下一页", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "上一页", exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: "../test-results/logs-pagination-desktop.png" })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole("button", { name: "上一页", exact: true }).scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: "../test-results/logs-pagination-mobile.png" })
  const downloadEvent = page.waitForEvent("download")
  await page.getByRole("button", { name: "导出 JSON", exact: true }).click()
  const stream = await (await downloadEvent).createReadStream()
  let text = ""
  for await (const chunk of stream) text += chunk.toString()
  expect(JSON.parse(text)).toHaveLength(225)
  await page.getByRole("textbox", { name: "搜索请求日志" }).fill("model-224")
  await expect(page.locator("tbody tr")).toHaveCount(1)
  await expect(page.locator("tbody tr").first()).toContainText("model-224")
  await page.getByRole("textbox", { name: "搜索请求日志" }).fill("")
  await expect(page.locator("tbody tr")).toHaveCount(100)
  await expect(page.locator("tbody tr").first()).toContainText("model-0")
})
