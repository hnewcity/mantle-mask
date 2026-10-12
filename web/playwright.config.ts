import { defineConfig } from "@playwright/test"
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 45000,
  use: {
    baseURL: "http://127.0.0.1:18080",
    viewport: { width: 1440, height: 1060 },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "node ../scripts/e2e-server.mjs",
    url: "http://127.0.0.1:18080/healthz",
    reuseExistingServer: false,
    timeout: 90000,
  },
})
