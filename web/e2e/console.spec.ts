import { expect, test } from "@playwright/test"

test("console navigation, responsive layouts, dialogs, and reduced motion", async ({ page }) => {
  const errors: string[] = []
  page.on("pageerror", error => errors.push(error.message))
  await page.emulateMedia({ reducedMotion: "reduce" })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/")
  await expect(page.getByRole("heading", { name: "登录管理控制台" })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await expect(page.locator(".liquid-flow")).toHaveCSS("animation-name", "none")
  await page.screenshot({ path: "../test-results/login-mobile.png", fullPage: true })
  await page.getByLabel("管理员令牌").fill("e2e-admin-token-only-for-local-tests")
  await page.getByRole("button", { name: "进入控制台" }).click()
  await expect(page.getByRole("heading", { name: "网关总览", exact: true })).toBeVisible()
  const skip = page.getByRole("link", { name: "跳转到内容" })
  await skip.focus()
  await expect(skip).toHaveCSS("opacity", "1")
  await skip.press("Enter")
  await expect(page.getByRole("main")).toBeFocused()

  const routes = [
    ["总览", "网关总览"],
    ["上游端点", "上游端点"],
    ["用户管理", "用户管理"],
    ["用量统计", "用量统计"],
    ["API 密钥", "API 密钥"],
    ["在线测试", "在线测试"],
    ["请求日志", "请求日志"],
    ["网关设置", "网关设置"],
  ]
  for (const width of [1440, 820, 390, 360]) {
    await page.setViewportSize({ width, height: 1000 })
    for (const [link, heading] of routes) {
      const navigation = page.getByRole("navigation", { name: "管理端导航" }).getByRole("link", { name: link, exact: true })
      await navigation.click()
      await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible()
      await expect(navigation).toHaveAttribute("aria-current", "page")
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${link} at ${width}px`).toBe(true)
    }
  }

  await page.getByRole("link", { name: "上游端点", exact: true }).click()
  await page.getByRole("button", { name: "添加上游", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toBeVisible()
  const bounds = await dialog.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(360)
  expect(bounds!.y).toBeGreaterThanOrEqual(0)
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(1000)
  await expect(page.getByLabel("端点名称")).toBeFocused()
  await page.screenshot({ path: "../test-results/upstream-dialog-mobile.png", fullPage: true })
  await page.keyboard.press("Escape")
  await expect(dialog).not.toBeVisible()
  await expect(page.getByRole("button", { name: "添加上游", exact: true })).toBeFocused()
  expect(errors).toEqual([])
})
