// 真實瀏覽器的 e2e（docs/ui/README.md 10.32）：Chromium、Firefox、WebKit 對真正建好的 notesteme binary
// （內嵌這裡的 dist）執行 serve。資料由 internal/benchdata 的 genlib 與 e2eseed 產生，不連任何外部網路
// （NFR-TEST-1：瀏覽器端另外在 e2e/support/test.ts 攔下所有非 127.0.0.1 的請求）。
//
//   npm run build && (cd ../.. && go build -o web/client/e2e/.out/notesteme ./cmd/notesteme)
//   npm run e2e -- --project=chromium
//
// 環境變數：
//   NOTESTEME_E2E_BIN   已建好的 binary（預設 e2e/.out/notesteme）
//   NOTESTEME_E2E_PORT  serve 的埠（預設 18181；10,000 列的 perf 另外用 +1）
//   NOTESTEME_E2E_SHOTS 設為 1 時截圖集寫到 e2e/.out/screenshots（browser.yml 上傳成 artifact）
import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.NOTESTEME_E2E_PORT ?? 18181);

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.spec.ts",
  outputDir: "./e2e/.out/test-results",
  globalSetup: "./e2e/support/global-setup.ts",
  // 機器與 runner 的記憶體有限（16GB WSL 上同時有其他重工作）：預設 2 個 worker。
  workers: Number(process.env.NOTESTEME_E2E_WORKERS ?? 2),
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // 不重試：不穩定就是缺陷，要修，不要用重試蓋過去。
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  reporter: process.env.CI
    ? [["list"], ["html", { outputFolder: "./e2e/.out/report", open: "never" }], ["github"]]
    : [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    storageState: "./e2e/.out/state/session.json",
    locale: "zh-TW",
    timezoneId: "Asia/Taipei",
    // PWA 的 service worker 不快取任何東西（ENTRY-R3），但會讓請求經過 SW：字型與 LCP 的量測要看到網路的原始請求。
    serviceWorkers: "block",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 800 } },
    },
    {
      name: "firefox",
      use: { ...devices["Desktop Firefox"], viewport: { width: 1280, height: 800 } },
    },
    {
      name: "webkit",
      use: { ...devices["Desktop Safari"], viewport: { width: 1280, height: 800 } },
    },
  ],
});
