import { expect, test } from "@playwright/test"
import { readFile } from "node:fs/promises"

const admin = { Authorization: "Bearer e2e-admin-token-only-for-local-tests" }
const password = "migration-test-password-123"

test("encrypted backup UI restores keys, upstreams, settings and complete usage", async ({ page }, testInfo) => {
  const errors: string[] = []
  page.on("pageerror", error => errors.push(error.message))
  const original = await page.request.post("/api/admin/backup/export", { headers: admin, data: { password } })
  expect(original.ok()).toBe(true)
  const baseline = await original.body()
  const restore = async (buffer: Buffer) => page.request.post("/api/admin/backup/restore", {
    headers: admin, multipart: { password, confirm: "RESTORE", file: { name: "backup.mmbak", mimeType: "application/octet-stream", buffer } },
  })
  try {
    const user = await (await page.request.post("/api/admin/users", { headers: admin, data: { name: "备份迁移测试用户" } })).json()
    const key = await (await page.request.post("/api/admin/keys", { headers: admin, data: { name: "迁移后继续使用", user_id: user.id } })).json()
    const upstream = await (await page.request.post("/api/admin/upstreams", { headers: admin, data: {
      name: "备份测试上游", base_url: "http://127.0.0.1:18090/v1", region: "us-east-1", auth_mode: "api_key", api_key: "mock-bedrock-key", weight: 1, enabled: true, models: { "backup-model": "mock-model" },
    } })).json()
    expect((await page.request.post("/v1/responses", { headers: { Authorization: `Bearer ${key.token}` }, data: { model: "backup-model", input: "backup test" } })).ok()).toBe(true)
    const before = await (await page.request.get("/api/admin/usage?all=true", { headers: admin })).json()
    const settings = await (await page.request.get("/api/admin/settings", { headers: admin })).json()
    await page.goto("/")
    await page.getByLabel("管理员令牌").fill("e2e-admin-token-only-for-local-tests")
    await page.getByRole("button", { name: "进入控制台" }).click()
    await expect(page.getByRole("heading", { name: "网关总览", exact: true })).toBeVisible()
    await page.goto("/#/settings")
    const panel = page.getByRole("region", { name: "备份与迁移" })
    await expect(panel).toBeVisible()
    await panel.getByLabel("备份密码", { exact: true }).fill("short")
    await expect(panel.getByRole("button", { name: "导出加密备份" })).toBeDisabled()
    await panel.getByLabel("备份密码", { exact: true }).fill(password)
    await panel.getByLabel("再次输入备份密码").fill("wrong")
    await expect(panel.getByRole("alert")).toContainText("不一致")
    await panel.getByLabel("再次输入备份密码").fill(password)
    const downloadPromise = page.waitForEvent("download")
    await panel.getByRole("button", { name: "导出加密备份" }).click()
    const download = await downloadPromise
    const backupPath = testInfo.outputPath("migration.mmbak")
    await download.saveAs(backupPath)
    const archive = await readFile(backupPath)
    expect(archive.includes(Buffer.from("mock-bedrock-key"))).toBe(false)
    expect(archive.includes(Buffer.from(key.token))).toBe(false)
    await expect(panel.getByRole("status")).toContainText("已生成加密备份")
    await expect(panel.getByLabel("备份密码", { exact: true })).toHaveValue("")

    expect((await page.request.delete(`/api/admin/keys/${key.key.id}`, { headers: admin })).ok()).toBe(true)
    expect((await page.request.delete(`/api/admin/upstreams/${upstream.id}`, { headers: admin })).ok()).toBe(true)
    const extra = await (await page.request.post("/api/admin/keys", { headers: admin, data: { name: "恢复前的额外 Key" } })).json()
    expect((await page.request.put("/api/admin/settings", { headers: admin, data: { ...settings, max_retries: 5 } })).ok()).toBe(true)
    await page.reload()
    await expect(page.getByLabel("最大重试次数", { exact: true })).toHaveValue("5")
    await panel.getByLabel("备份文件").setInputFiles(backupPath)
    await panel.getByLabel("解密密码").fill("wrong-password-long")
    await panel.getByRole("button", { name: "恢复备份", exact: true }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.getByRole("button", { name: "覆盖并恢复", exact: true })).toBeDisabled()
    await dialog.getByLabel("输入 RESTORE 确认覆盖").fill("RESTORE")
    await dialog.getByRole("button", { name: "覆盖并恢复", exact: true }).click()
    await expect(panel.getByRole("alert")).toContainText("备份密码错误或文件已损坏")
    expect((await page.request.get("/api/me", { headers: { Authorization: `Bearer ${key.token}` } })).status()).toBe(401)
    await expect(page.getByLabel("最大重试次数", { exact: true })).toHaveValue("5")

    await panel.getByLabel("解密密码").fill(password)
    await panel.getByRole("button", { name: "恢复备份", exact: true }).click()
    await dialog.getByRole("button", { name: "取消", exact: true }).click()
    expect((await page.request.get("/api/me", { headers: { Authorization: `Bearer ${extra.token}` } })).ok()).toBe(true)
    await panel.getByRole("button", { name: "恢复备份", exact: true }).click()
    await dialog.getByLabel("输入 RESTORE 确认覆盖").fill("RESTORE")
    await dialog.getByRole("button", { name: "覆盖并恢复", exact: true }).click()
    await expect(panel.getByRole("status")).toContainText("恢复成功")
    await expect(page.getByLabel("最大重试次数", { exact: true })).toHaveValue(String(settings.max_retries))
    expect((await page.request.get("/api/me", { headers: { Authorization: `Bearer ${key.token}` } })).ok()).toBe(true)
    expect((await page.request.get("/api/me", { headers: { Authorization: `Bearer ${extra.token}` } })).status()).toBe(401)
    expect(await (await page.request.get("/api/admin/usage?all=true", { headers: admin })).json()).toEqual(before)
    expect((await page.request.get("/v1/responses/resp_mock", { headers: { Authorization: `Bearer ${key.token}` } })).ok()).toBe(true)
    await page.getByRole("button", { name: "关闭通知" }).click()
    await panel.screenshot({ path: testInfo.outputPath("backup-desktop.png") })
    await page.setViewportSize({ width: 390, height: 844 })
    await panel.scrollIntoViewIfNeeded()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await panel.screenshot({ path: testInfo.outputPath("backup-mobile.png") })
    expect(errors).toEqual([])
  } finally {
    expect((await restore(baseline)).ok()).toBe(true)
  }
})
