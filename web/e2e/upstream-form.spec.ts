import { expect, test } from "@playwright/test"

const admin = { Authorization: "Bearer e2e-admin-token-only-for-local-tests" }

test.beforeEach(async ({ page }) => {
  await page.goto("/#/upstreams")
  await page.getByLabel("管理员令牌").fill("e2e-admin-token-only-for-local-tests")
  await page.getByRole("button", { name: "进入控制台" }).click()
  await page.getByRole("button", { name: "添加上游", exact: true }).click()
})

test("upstream dialog has one scroll area with a fixed header and actions", async ({ page }) => {
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: /AWS SigV4/ }).click()
  await dialog.getByRole("radio", { name: /指定模型/ }).check()
  for (let i = 0; i < 4; i++) await dialog.getByRole("button", { name: "添加模型", exact: true }).click()

  for (const [width, height] of [[811, 889], [811, 600], [390, 844], [360, 640]]) {
    await page.setViewportSize({ width, height })
    await dialog.getByLabel("端点名称", { exact: true }).focus()
    await expect(dialog.getByLabel("端点名称", { exact: true })).toHaveCSS("outline-offset", "-2px")
    const header = await dialog.locator(".upstream-dialog-header").boundingBox()
    const footer = await dialog.locator(".upstream-dialog-footer").boundingBox()
    const scrollAreas = await dialog.evaluate(el => [el, ...el.querySelectorAll("*")].filter(node => {
      const style = getComputedStyle(node)
      return /^(auto|scroll)$/.test(style.overflowY) && node.scrollHeight > node.clientHeight + 1
    }).map(node => node.className))
    expect(scrollAreas, `${width} × ${height}`).toEqual(["dialog-scroll"])
    expect(await dialog.evaluate(el => el.scrollHeight <= el.clientHeight + 1)).toBe(true)
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
    expect(header!.y).toBeGreaterThanOrEqual(0)
    expect(footer!.y + footer!.height).toBeLessThanOrEqual(height)
    await dialog.getByLabel("上游模型 ID 5", { exact: true }).focus()
    expect((await dialog.locator(".upstream-dialog-header").boundingBox())!.y).toBeCloseTo(header!.y)
    expect((await dialog.locator(".upstream-dialog-footer").boundingBox())!.y).toBeCloseTo(footer!.y)
    await expect(dialog.getByRole("button", { name: "保存上游", exact: true })).toBeVisible()
    if (width === 811 && height === 889) await page.screenshot({ path: "../test-results/upstream-mapping-editor.png" })
    if (width === 390) await page.screenshot({ path: "../test-results/upstream-mapping-editor-mobile.png" })
  }
})

test("model rows validate, persist, restore on edit, and explicitly switch to passthrough", async ({ page }) => {
  await page.setViewportSize({ width: 811, height: 889 })
  const dialog = page.getByRole("dialog")
  const writes: string[] = []
  page.on("request", request => {
    if (request.method() === "POST" && request.url().endsWith("/api/admin/upstreams")) writes.push(request.url())
  })
  await dialog.getByLabel("端点名称").fill("Mapping editor fixture")
  await dialog.getByLabel("Base URL", { exact: true }).fill("http://127.0.0.1:18090/v1")
  await dialog.getByLabel("Bedrock API Key", { exact: true }).fill("mock-bedrock-key")
  await dialog.getByRole("radio", { name: /指定模型/ }).check()
  await dialog.getByLabel("对外名称 1", { exact: true }).fill(" chat ")
  await dialog.getByRole("button", { name: "保存上游" }).click()
  await expect(dialog.getByLabel("上游模型 ID 1", { exact: true })).toHaveAttribute("aria-invalid", "true")
  await expect(dialog.getByLabel("上游模型 ID 1", { exact: true })).toBeFocused()
  expect(writes).toHaveLength(0)

  await dialog.getByLabel("上游模型 ID 1", { exact: true }).fill(" mock-model ")
  await dialog.getByRole("button", { name: "添加模型", exact: true }).click()
  await expect(dialog.getByLabel("对外名称 2", { exact: true })).toBeFocused()
  await dialog.getByLabel("对外名称 2", { exact: true }).fill("chat")
  await dialog.getByLabel("上游模型 ID 2", { exact: true }).fill("second-model")
  await dialog.getByRole("button", { name: "保存上游" }).click()
  await expect(dialog.getByLabel("对外名称 1", { exact: true })).toHaveAttribute("aria-invalid", "true")
  await expect(dialog.getByLabel("对外名称 2", { exact: true })).toHaveAttribute("aria-invalid", "true")
  expect(writes).toHaveLength(0)
  await dialog.getByLabel("对外名称 2", { exact: true }).fill("responses")
  await page.screenshot({ path: "../test-results/upstream-mapping-fixed.png" })

  await dialog.getByText("路由与项目", { exact: true }).click()
  await dialog.getByLabel("路由权重", { exact: true }).fill("0")
  await dialog.getByText("路由与项目", { exact: true }).click()
  await dialog.getByRole("button", { name: "保存上游" }).click()
  await expect(dialog.locator(".upstream-advanced")).toHaveAttribute("open", "")
  await expect(dialog.getByLabel("路由权重", { exact: true })).toBeFocused()
  await dialog.getByLabel("路由权重", { exact: true }).fill("3")

  await dialog.getByRole("radio", { name: /全部透传/ }).check()
  await expect(dialog.getByLabel("对外名称 1", { exact: true })).toHaveCount(0)
  await dialog.getByRole("radio", { name: /指定模型/ }).check()
  await expect(dialog.getByLabel("对外名称 1", { exact: true })).toHaveValue(" chat ")
  const created = page.waitForResponse(response => response.request().method() === "POST" && response.url().endsWith("/api/admin/upstreams"))
  await dialog.getByRole("button", { name: "保存上游" }).click()
  const upstream = await (await created).json()
  try {
    const saved = await (await page.request.get("/api/admin/upstreams", { headers: admin })).json()
    const persisted = saved.data.find((item: { id: string }) => item.id === upstream.id)
    expect(persisted.models).toEqual({ chat: "mock-model", responses: "second-model" })
    expect(persisted.weight).toBe(3)
    await expect(dialog).not.toBeVisible()
    await page.getByRole("button", { name: "编辑 Mapping editor fixture", exact: true }).click()
    await expect(dialog.getByRole("radio", { name: /指定模型/ })).toBeChecked()
    await expect(dialog.getByLabel("对外名称 1", { exact: true })).toHaveValue("chat")
    await expect(dialog.getByLabel("上游模型 ID 2", { exact: true })).toHaveValue("second-model")
    await dialog.getByRole("button", { name: "删除模型映射 2", exact: true }).click()
    await dialog.getByRole("button", { name: "删除模型映射 1", exact: true }).click()
    await dialog.getByRole("button", { name: "保存上游" }).click()
    await expect(dialog.getByRole("alert")).toContainText("至少添加一个模型")
    await expect(dialog.getByRole("radio", { name: /指定模型/ })).toBeChecked()
    await dialog.getByRole("radio", { name: /全部透传/ }).check()
    await dialog.getByRole("button", { name: "保存上游" }).click()
    await expect(dialog).not.toBeVisible()
    const result = await (await page.request.get("/api/admin/upstreams", { headers: admin })).json()
    expect(result.data.find((item: { id: string }) => item.id === upstream.id).models).toEqual({})
  } finally {
    await page.request.delete(`/api/admin/upstreams/${upstream.id}`, { headers: admin })
  }
})
