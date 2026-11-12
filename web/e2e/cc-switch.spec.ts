import { expect, test, type Page } from "@playwright/test"

const adminToken = "e2e-admin-token-only-for-local-tests"
const admin = { Authorization: `Bearer ${adminToken}` }
type Captures = { ccSwitchLinks: string[]; copiedLink: string }

async function captureImports(page: Page) {
  // Capture the OS handoff without launching a locally installed CC Switch.
  await page.addInitScript(() => {
    const captures = window as unknown as Captures
    captures.ccSwitchLinks = []
    const click = HTMLAnchorElement.prototype.click
    HTMLAnchorElement.prototype.click = function () {
      if (this.protocol === "ccswitch:") { captures.ccSwitchLinks.push(this.href); return }
      click.call(this)
    }
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
      writeText: async (value: string) => { captures.copiedLink = value },
    } })
  })
}

async function imported(page: Page) {
  const links = await page.evaluate(() => (window as unknown as Captures).ccSwitchLinks)
  return new URL(links.at(-1)!).searchParams
}

test("new key imports the gateway credential and selected model into CC Switch", async ({ page }) => {
  await captureImports(page)
  const errors: string[] = []
  page.on("pageerror", error => errors.push(error.message))
  let modelAuth = ""
  await page.route("**/v1/models", route => {
    modelAuth = route.request().headers().authorization
    return route.fulfill({ json: { data: [{ id: "team-coder", object: "model", owned_by: "fixture" }] } })
  })
  await page.goto("/")
  await page.getByLabel("管理员令牌").fill(adminToken)
  await page.getByRole("button", { name: "进入控制台" }).click()
  await page.getByRole("link", { name: "API 密钥", exact: true }).click()
  await page.getByRole("button", { name: "创建密钥", exact: true }).first().click()
  await page.getByLabel("密钥名称").fill("CC Switch 开发")
  await page.getByRole("dialog").getByRole("button", { name: "创建密钥", exact: true }).click()
  await expect(page.getByRole("heading", { name: "API 密钥已创建" })).toBeVisible()
  const token = await page.locator(".created-key code").innerText()
  await expect(page.getByRole("combobox", { name: "接入模型", exact: true })).toContainText("team-coder")
  expect(modelAuth).toBe(`Bearer ${token}`)
  await page.getByLabel("供应商名称", { exact: true }).fill("Mantle Mask · 开发 + QA & 测试")
  await page.getByRole("button", { name: "一键导入 CC Switch", exact: true }).click()
  const codex = await imported(page)
  expect(codex.get("resource")).toBe("provider")
  expect(codex.get("app")).toBe("codex")
  expect(codex.get("apiFormat")).toBe("openai_responses")
  expect(codex.get("apiKey")).toBe(token)
  expect(codex.get("apiKey")).not.toBe(adminToken)
  expect(codex.get("endpoint")).toBe("http://127.0.0.1:18080/v1")
  expect(codex.get("name")).toBe("Mantle Mask · 开发 + QA & 测试")
  expect(codex.get("model")).toBe("team-coder")
  await page.getByRole("button", { name: "复制导入链接" }).click()
  const copied = await page.evaluate(() => (window as unknown as Captures).copiedLink)
  expect(new URL(copied).search).toBe(`?${codex}`)
  await expect(page.locator('a[href^="ccswitch:"]')).toHaveCount(0)
  await page.screenshot({ path: "../test-results/cc-switch-created-key-desktop.png", fullPage: true })
  await page.getByRole("button", { name: "我已保存" }).click()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(await page.evaluate(() => sessionStorage.getItem("mantle-admin"))).toBe(adminToken)
  expect(await page.evaluate(() => Object.values(sessionStorage))).not.toContain(token)
  expect(errors).toEqual([])
})

test("user portal supports manual models and Claude routing without model discovery", async ({ page }) => {
  await captureImports(page)
  const user = await (await page.request.post("/api/admin/users", { headers: admin, data: { name: "CC Switch 用户" } })).json()
  const key = await (await page.request.post("/api/admin/keys", { headers: admin, data: { name: "CC Switch 当前密钥", user_id: user.id } })).json()
  let modelAuth = ""
  await page.route("**/v1/models", route => {
    modelAuth = route.request().headers().authorization
    return route.fulfill({ status: 503, json: { error: { message: "Fixture unavailable" } } })
  })
  await page.goto("/")
  await page.getByRole("button", { name: "用户用量查询", exact: true }).click()
  await page.getByLabel("API Key", { exact: true }).fill(key.token)
  await page.getByRole("button", { name: "查看用量", exact: true }).click()
  await page.getByRole("button", { name: "导入 CC Switch", exact: true }).click()
  await expect(page.getByText("无法读取模型列表，请手动填写模型 ID / 对外别名。")).toBeVisible()
  expect(modelAuth).toBe(`Bearer ${key.token}`)
  await expect(page.getByRole("button", { name: "一键导入 CC Switch", exact: true })).toBeDisabled()
  await expect(page.getByRole("button", { name: "复制导入链接" })).toBeDisabled()
  await page.getByRole("combobox", { name: "目标客户端", exact: true }).click()
  await page.getByRole("option", { name: "Claude Code", exact: true }).click()
  await expect(page.getByText(/Claude Code 需要 CC Switch v4.0.10/)).toBeVisible()
  await page.getByRole("combobox", { name: "接入模型", exact: true }).click()
  await page.getByRole("combobox", { name: "搜索接入模型", exact: true }).fill("custom/coder:latest")
  await page.getByRole("combobox", { name: "搜索接入模型", exact: true }).press("Enter")
  await page.getByRole("button", { name: "一键导入 CC Switch", exact: true }).click()
  const claude = await imported(page)
  expect(claude.get("apiKey")).toBe(key.token)
  expect(claude.get("app")).toBe("claude")
  expect(claude.get("apiFormat")).toBe("openai_chat")
  for (const field of ["model", "haikuModel", "sonnetModel", "opusModel"]) expect(claude.get(field)).toBe("custom/coder:latest")
  expect(claude.has("enabled")).toBe(false)
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  const dialog = page.getByRole("dialog", { name: "导入 CC Switch", exact: true })
  expect(await dialog.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
  await page.screenshot({ path: "../test-results/cc-switch-user-mobile.png", fullPage: true })
  await page.getByRole("button", { name: "关闭", exact: true }).click()
  await expect(page.getByRole("button", { name: "导入 CC Switch", exact: true })).toBeFocused()
  await page.getByRole("button", { name: "退出登录", exact: true }).click()
  await expect(page.getByRole("button", { name: "导入 CC Switch", exact: true })).toHaveCount(0)
})
