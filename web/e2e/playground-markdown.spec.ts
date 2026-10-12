import { expect, test } from "@playwright/test"

const headers = { Authorization: "Bearer e2e-admin-token-only-for-local-tests" }
let upstreamID = ""

test.beforeAll(async ({ request }) => {
  const setup = await request.post("/api/admin/upstreams", {
    headers,
    data: { name: "Markdown fixture", base_url: "http://127.0.0.1:18090/v1", region: "us-east-1", auth_mode: "api_key", api_key: "mock-bedrock-key", weight: 1, enabled: true, models: {} },
  })
  expect(setup.ok()).toBe(true)
  upstreamID = (await setup.json()).id
})

test.afterAll(async ({ request }) => {
  if (upstreamID) await request.delete(`/api/admin/upstreams/${upstreamID}`, { headers })
})

for (const endpoint of ["Chat Completions", "Responses"]) {
  for (const stream of [true, false]) {
    test(`${endpoint} renders Markdown with stream=${stream} and preserves raw output`, async ({ page }) => {
      const errors: string[] = []
      page.on("pageerror", error => errors.push(error.message))
      await page.goto("/")
      await page.getByLabel("管理员令牌").fill("e2e-admin-token-only-for-local-tests")
      await page.getByRole("button", { name: "进入控制台" }).click()
      await page.getByRole("link", { name: "在线测试", exact: true }).click()
      await expect(page.getByRole("combobox", { name: "模型", exact: true })).toContainText("mock-model")
      if (endpoint === "Responses") {
        await page.getByRole("combobox", { name: "API 接口" }).click()
        await page.getByRole("option", { name: endpoint, exact: true }).click()
      }
      if (!stream) await page.getByRole("switch", { name: "流式输出" }).click()
      const prompt = "[markdown-fixture] **保持原文**"
      await page.getByLabel("输入提示词").fill(prompt)
      await page.getByRole("button", { name: "发送请求" }).click()
      const output = page.locator(".chat-message.assistant .markdown-content")
      await expect(output.getByRole("heading", { name: "Markdown 回复" })).toBeVisible()
      if (stream) {
        await expect(page.getByRole("button", { name: "停止生成" })).toBeVisible()
        await expect(page.getByText("响应完成", { exact: true })).toHaveCount(0)
      }
      await expect(page.getByText("响应完成", { exact: true })).toBeVisible()
      await expect(page.locator(".chat-message.user .chat-content")).toHaveText(prompt)
      await expect(page.locator(".chat-message.user .markdown-content")).toHaveCount(0)
      await expect(output.locator("strong").first()).toHaveText("加粗")
      await expect(output.locator("strong").first()).toHaveCSS("display", "inline")
      await expect(output.locator("strong").first()).toHaveCSS("font-weight", "600")
      await expect(output.locator("em")).toHaveText("斜体")
      await expect(output.locator("del")).toHaveText("删除线")
      await expect(output.locator("ul ul li")).toHaveText("嵌套项")
      await expect(output.locator("ol li")).toHaveCount(2)
      await expect(output.locator("blockquote")).toContainText("引用内容")
      await expect(output.locator("pre code.language-go")).toContainText('fmt.Println("<hello>")')
      await expect(output.getByRole("cell", { name: "可用", exact: true })).toBeVisible()
      await expect(output.getByRole("checkbox").first()).toBeChecked()
      await expect(output.getByRole("checkbox").first()).toBeDisabled()
      await expect(output.getByRole("link", { name: "文档", exact: true })).toHaveAttribute("rel", "noopener noreferrer")
      await page.locator(".conversation").evaluate(element => element.scrollTop = 0)
      if (endpoint === "Chat Completions" && stream) {
        await page.screenshot({ path: "../test-results/playground-markdown-desktop.png", fullPage: true })
        await page.locator(".conversation").evaluate(element => element.scrollTop = element.scrollHeight)
        await page.screenshot({ path: "../test-results/playground-markdown-blocks-desktop.png", fullPage: true })
        await page.locator(".conversation").evaluate(element => element.scrollTop = 0)
      }
      for (const width of [390, 360]) {
        await page.setViewportSize({ width, height: 844 })
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        expect(await output.locator("pre").evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true)
        expect(await output.locator(".markdown-table").evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true)
        if (endpoint === "Chat Completions" && stream) {
          await page.screenshot({ path: `../test-results/playground-markdown-${width}.png`, fullPage: true })
          await page.locator(".conversation").evaluate(element => element.scrollTop = element.scrollHeight)
          await page.screenshot({ path: `../test-results/playground-markdown-blocks-${width}.png`, fullPage: true })
          await page.locator(".conversation").evaluate(element => element.scrollTop = 0)
        }
      }
      await page.setViewportSize({ width: 1440, height: 1060 })
      await page.getByRole("button", { name: "原始响应", exact: true }).click()
      await expect(page.locator(".raw-output pre")).toContainText("# Markdown 回复")
      await expect(page.locator(".raw-output h1")).toHaveCount(0)
      await expect(output).toHaveCount(0)
      await page.getByRole("button", { name: "模型输出", exact: true }).click()
      await expect(output.getByRole("heading", { name: "Markdown 回复" })).toBeVisible()

      // Formatting must never replace the source text used for subsequent turns.
      const pending = page.waitForRequest(request => request.method() === "POST" && request.url().includes("/api/admin/playground/"))
      await page.getByLabel("输入提示词").fill("继续")
      await page.getByRole("button", { name: "发送请求" }).click()
      const body = (await pending).postDataJSON()
      const history = body.messages || body.input
      expect(history[1].role).toBe("assistant")
      expect(history[1].content).toContain("**加粗**")
      expect(history[1].content).toContain("```go")
      await expect(page.getByText("响应完成", { exact: true })).toBeVisible()
      await page.getByRole("button", { name: "清空", exact: true }).click()
      await expect(output).toHaveCount(0)
      expect(errors).toEqual([])
    })
  }
}
