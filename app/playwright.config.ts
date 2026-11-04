import { defineConfig } from "@playwright/test";

// e2e 在 Vite 开发服务器上运行前端，后端是 globalSetup 启动的真实测试服务
// （FreeRDP 示例服务器 13390 + remoter-mcp 18766，数据目录 /tmp/rdptest-ui），与其他测试环境互不干扰。
const PORT = 14210;

export default defineConfig({
  testDir: "e2e",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  workers: 1,
  fullyParallel: false,
  reporter: [["list"]],
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    viewport: { width: 1360, height: 860 },
    trace: "retain-on-failure",
    // 默认使用 Playwright 自带的 Chromium；E2E_CHANNEL=chrome 可改用本机已安装的 Chrome（无需下载）
    ...(process.env.E2E_CHANNEL ? { channel: process.env.E2E_CHANNEL } : {}),
  },
  webServer: {
    command: `pnpm exec vite --port ${PORT} --strictPort`,
    url: `http://127.0.0.1:${PORT}`,
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
