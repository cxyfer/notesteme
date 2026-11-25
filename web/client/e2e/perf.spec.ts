// NFR-PERF-10：本機連線的 LCP < 2 秒；媒體庫列表虛擬化，捲動 10,000 列不掉幀。
//   - LCP：全新的 context（沒有快取）載入 /library，PerformanceObserver 的 largest-contentful-paint，量 3 次取中位數。
//     門檻 2 秒不放寬；引擎不支援 LCP 時以具名的 fixme 標出（docs/ui/README.md 10.32）。
//   - 捲動：「不掉幀」的判定（docs/ui/README.md 10.32 的決定，沿用人工驗收清單第 9 節的寫法）：
//       1. 捲動期間沒有超過 50ms 的長工作（Chromium 的 longtask；其他引擎以 rAF 間隔代替）；
//       2. 沒有「持續掉幀」：連續 3 個以上的 frame 間隔超過 50ms（約 3 個 60Hz frame）；
//       3. DOM 中的列數遠少於總數（虛擬化）。
//     另外記錄 rAF 間隔的 p95 與超過 33.4ms 的比例。
//   - 頁面持續掉幀時，再在同一個引擎捲動一份靜態清單當基準：基準也持續掉幀代表引擎在這台機器上
//     量不出來（CI 的 WebKit 是軟體繪製），以具名的 fixme 標出，數值照樣記錄（docs/ui/README.md 10.32）。
//   - 預設用 global setup 的 200 部（驗證虛擬化與捲動到底）；@perf10k 是真實 10,000 列的一次實測，
//     資料由 genlib -cues 4 產生（列表不讀字幕），只在 NOTESTEME_E2E_PERF10K=1 時執行（browser.yml 的 perf job）。
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";

import type { Browser, Page, TestInfo } from "@playwright/test";

import { login, outDir, startServe, type Served } from "./support/server";
import { expect, test, waitReady } from "./support/test";

interface Result {
  case: string;
  engine: string;
  value: number;
  threshold: number;
  unit: string;
  pass: boolean;
  note?: string;
}

function record(info: TestInfo, r: Omit<Result, "engine">) {
  const full = { ...r, engine: info.project.name };
  const dir = path.join(outDir, "perf");
  mkdirSync(dir, { recursive: true });
  writeFileSync(
    path.join(dir, `${info.project.name}-${r.case.replace(/\W+/g, "_")}.json`),
    JSON.stringify(full),
  );
  console.log(`PERF ${JSON.stringify(full)}`);
}

/**
 * 全新的 context（沒有快取）；外部網路一律擋下（NFR-TEST-1，同 e2e/support/test.ts：Firefox 用 proxy，不攔截）
 */
async function offlineContext(browser: Browser, base: string, storage: string) {
  const firefox = browser.browserType().name() === "firefox";
  const ctx = await browser.newContext({
    baseURL: base,
    storageState: storage,
    viewport: { width: 1280, height: 800 },
    serviceWorkers: "block",
    ...(firefox ? { proxy: { server: "http://127.0.0.1:9", bypass: "127.0.0.1,localhost" } } : {}),
  });
  if (!firefox) {
    await ctx.route(
      (u) => u.hostname !== "127.0.0.1",
      (r) => r.abort("blockedbyclient"),
    );
  }
  return ctx;
}

/** 一次冷載入的 LCP（ms）；不支援時回傳 null */
async function lcpOnce(browser: Browser, base: string, storage: string, url: string) {
  const ctx = await offlineContext(browser, base, storage);
  const page = await ctx.newPage();
  await page.addInitScript(() => {
    const w = window as unknown as { __lcp: number; __lcpEl: string };
    w.__lcp = -1;
    w.__lcpEl = "";
    if (!PerformanceObserver.supportedEntryTypes?.includes("largest-contentful-paint")) return;
    new PerformanceObserver((list) => {
      for (const e of list.getEntries() as (PerformanceEntry & { element?: Element })[]) {
        w.__lcp = e.startTime;
        w.__lcpEl = e.element
          ? `${e.element.tagName.toLowerCase()}.${e.element.getAttribute("class") ?? ""}`
          : "";
      }
    }).observe({ type: "largest-contentful-paint", buffered: true });
  });
  await page.goto(url);
  await waitReady(page, "媒體庫");
  // LCP 在第一次輸入或捲動前定案：等畫面穩定後讀取
  await page.waitForTimeout(1000);
  const r = await page.evaluate(() => {
    const w = window as unknown as { __lcp: number; __lcpEl: string };
    return { lcp: w.__lcp, el: w.__lcpEl };
  });
  await ctx.close();
  return r.lcp < 0 ? null : r;
}

/** 捲動列表到底（必要時一直載入更多），量 frame 間隔與長工作 */
async function scrollToBottom(page: Page, total: number, timeoutMs: number) {
  return page.evaluate(
    ([total, timeoutMs]) =>
      new Promise<{
        intervals: number[];
        longTasks: number[];
        longTaskSupported: boolean;
        maxRows: number;
        reached: number;
      }>((resolve) => {
        const list = document.querySelector<HTMLElement>(".vlist")!;
        const longTasks: number[] = [];
        const longTaskSupported = !!PerformanceObserver.supportedEntryTypes?.includes("longtask");
        let obs: PerformanceObserver | undefined;
        if (longTaskSupported) {
          obs = new PerformanceObserver((l) => {
            for (const e of l.getEntries()) longTasks.push(e.duration);
          });
          obs.observe({ type: "longtask" });
        }
        const intervals: number[] = [];
        let last = performance.now();
        const t0 = last;
        let maxRows = 0;
        const reachedIndex = () =>
          Math.max(
            0,
            ...[...list.querySelectorAll("[aria-posinset]")].map((e) =>
              Number(e.getAttribute("aria-posinset")),
            ),
          );
        const tick = (now: number) => {
          intervals.push(now - last);
          last = now;
          maxRows = Math.max(maxRows, list.querySelectorAll("[aria-posinset]").length);
          // 每個 frame 捲 120px（約 2 列；一般滾輪連續捲動的速度）
          list.scrollTop += 120;
          const reached = reachedIndex();
          // 虛擬列表：最後一列被渲染出來；也要真的捲到底（靜態清單的列一開始就全部在 DOM 裡）
          const atBottom = list.scrollTop + list.clientHeight >= list.scrollHeight - 1;
          if ((reached >= total && atBottom) || now - t0 > timeoutMs) {
            obs?.disconnect();
            resolve({
              intervals: intervals.slice(1),
              longTasks,
              longTaskSupported,
              maxRows,
              reached,
            });
          } else requestAnimationFrame(tick);
        };
        requestAnimationFrame(tick);
      }),
    [total, timeoutMs] as const,
  );
}

/** 最長的「連續超過 50ms 的 frame」 */
function worstStreakOf(intervals: number[]) {
  let streak = 0;
  let worst = 0;
  for (const d of intervals) {
    streak = d > 50 ? streak + 1 : 0;
    worst = Math.max(worst, streak);
  }
  return worst;
}

/**
 * 基準：同一個引擎、同一個視窗，捲動一份沒有 JS、沒有樣式的靜態清單（與媒體庫同樣的列數與列高）。
 * 用來分辨「頁面掉幀」與「這個引擎在這台機器上連最簡單的捲動都畫不到 60Hz」（CI 的 WebKit 是軟體繪製）。
 */
async function baselineScroll(page: Page, rows: number) {
  await page.setContent(
    `<div class="vlist" style="height:720px;overflow:auto">${Array.from(
      { length: rows },
      (_, i) =>
        `<div aria-posinset="${i + 1}" style="height:88px;border-bottom:1px solid #ccc">Video ${i} about databases<br>YouTube · 2025-01-01 · 12:34</div>`,
    ).join("")}</div>`,
  );
  const r = await scrollToBottom(page, rows, 60_000);
  const sorted = [...r.intervals].sort((a, b) => a - b);
  return {
    worstStreak: worstStreakOf(r.intervals),
    p95: sorted[Math.ceil(sorted.length * 0.95) - 1] ?? 0,
  };
}

function judgeScroll(
  info: TestInfo,
  label: string,
  total: number,
  r: Awaited<ReturnType<typeof scrollToBottom>>,
  baseline?: Awaited<ReturnType<typeof baselineScroll>>,
) {
  const sorted = [...r.intervals].sort((a, b) => a - b);
  const p95 = sorted[Math.ceil(sorted.length * 0.95) - 1] ?? 0;
  const slow = r.intervals.filter((d) => d > 33.4).length / Math.max(1, r.intervals.length);
  const worstStreak = worstStreakOf(r.intervals);
  const longest = Math.max(0, ...r.longTasks);
  const note = `${r.intervals.length} frames、p95 ${p95.toFixed(1)}ms、>33ms ${(slow * 100).toFixed(1)}%、最長 ${Math.max(...r.intervals).toFixed(0)}ms、長工作 ${r.longTaskSupported ? `${r.longTasks.length} 個（最長 ${longest.toFixed(0)}ms）` : "不支援"}、DOM 最多 ${r.maxRows} 列${baseline ? `；基準（靜態清單）連續掉幀 ${baseline.worstStreak}、p95 ${baseline.p95.toFixed(1)}ms` : ""}`;
  record(info, {
    case: `${label} 連續掉幀`,
    value: worstStreak,
    threshold: 3,
    unit: "frames",
    pass: worstStreak < 3,
    note,
  });
  if (r.longTaskSupported) {
    record(info, {
      case: `${label} 長工作`,
      value: Math.round(longest),
      threshold: 50,
      unit: "ms",
      pass: longest <= 50,
      note,
    });
  }
  expect(r.reached, `沒有捲到第 ${total} 列（到 ${r.reached}）`).toBeGreaterThanOrEqual(total);
  expect(r.maxRows, `DOM 裡有 ${r.maxRows} 列：沒有虛擬化`).toBeLessThan(Math.min(100, total / 2));
  // 引擎限制（docs/ui/README.md 10.32）：連靜態清單都持續掉幀時，這台機器上的這個引擎量不出頁面的流暢度。
  // 數值照樣記錄；判定以具名的 fixme 標出，不算通過
  test.fixme(
    worstStreak >= 3 && !!baseline && baseline.worstStreak >= 3,
    `${info.project.name} 在這台機器上捲動靜態清單也持續掉幀（基準 ${baseline?.worstStreak} 個 frame、p95 ${baseline?.p95.toFixed(1)}ms），量不出頁面的流暢度：${note}`,
  );
  expect(worstStreak, `持續掉幀：${note}`).toBeLessThan(3);
  if (r.longTaskSupported) expect(longest, `長工作超過 50ms：${note}`).toBeLessThanOrEqual(50);
}

test.describe("NFR-PERF-10", () => {
  test("LCP：冷載入媒體庫 3 次的中位數 < 2 秒", async ({ browser, baseURL }, info) => {
    const storage = path.join(outDir, "state", "session.json");
    const samples: { lcp: number; el: string }[] = [];
    for (let i = 0; i < 3; i++) {
      const r = await lcpOnce(browser, baseURL!, storage, "/library");
      test.fixme(
        r === null,
        `${info.project.name} 不支援 largest-contentful-paint（引擎限制，docs/ui/README.md 10.32）`,
      );
      samples.push(r!);
    }
    const sorted = samples.map((s) => s.lcp).sort((a, b) => a - b);
    const median = sorted[1]!;
    record(info, {
      case: "LCP /library（3 次中位數）",
      value: Math.round(median),
      threshold: 2000,
      unit: "ms",
      pass: median < 2000,
      note: `樣本 ${sorted.map((v) => Math.round(v)).join("、")}ms；LCP 元素 ${samples[1]!.el}`,
    });
    expect(median, `LCP 中位數 ${median}ms`).toBeLessThan(2000);
  });

  test("捲動：200 部的媒體庫捲到底不掉幀、只渲染可視範圍", async ({ page }, info) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto("/library?group=none");
    await waitReady(page, "媒體庫");
    const total = Number(await page.locator("[aria-setsize]").first().getAttribute("aria-setsize"));
    expect(total).toBeGreaterThan(150);
    const r = await scrollToBottom(page, total, 60_000);
    // 頁面掉幀時才量基準（同一個 page，量完頁面才換內容）。另外記錄 reduce 時同一頁的結果，
    // 分辨是否來自無限的動畫（顯影、spinner）在軟體繪製下持續重繪（只記錄，不影響判定）
    let baseline: Awaited<ReturnType<typeof baselineScroll>> | undefined;
    if (worstStreakOf(r.intervals) >= 3) {
      await page.emulateMedia({ reducedMotion: "reduce" });
      await page.goto("/library?group=none");
      await waitReady(page, "媒體庫");
      const reduced = await scrollToBottom(page, total, 60_000);
      const sortedReduced = [...reduced.intervals].sort((a, b) => a - b);
      console.log(
        `PERF-DIAG ${info.project.name} reduce 時的同一頁：連續掉幀 ${worstStreakOf(reduced.intervals)}、p95 ${(sortedReduced[Math.ceil(sortedReduced.length * 0.95) - 1] ?? 0).toFixed(1)}ms`,
      );
      baseline = await baselineScroll(page, total);
    }
    judgeScroll(info, `捲動 ${total} 列`, total, r, baseline);
  });
});

test.describe("NFR-PERF-10 @perf10k", () => {
  test.skip(
    process.env.NOTESTEME_E2E_PERF10K !== "1",
    "10,000 列只在 browser.yml 的 perf job 執行",
  );
  let served: Served | undefined;
  test.beforeAll(async () => {
    test.setTimeout(20 * 60_000);
    served = await startServe({
      port: Number(process.env.NOTESTEME_E2E_PORT ?? 18181) + 1,
      data: { scale: 1, cues: 4, seed: false },
      dataDir: process.env.NOTESTEME_E2E_DATA10K,
    });
  });
  test.afterAll(async () => {
    await served?.stop();
  });

  test("LCP 與捲動：真實 10,000 列", async ({ browser }, info) => {
    test.setTimeout(15 * 60_000);
    const state = await login(served!.base);
    const storage = path.join(outDir, "state", "session10k.json");
    writeFileSync(storage, JSON.stringify(state));
    const samples: number[] = [];
    for (let i = 0; i < 3; i++) {
      const r = await lcpOnce(browser, served!.base, storage, "/library?group=none");
      if (r) samples.push(r.lcp);
    }
    if (samples.length === 3) {
      const median = samples.sort((a, b) => a - b)[1]!;
      record(info, {
        case: "LCP /library 10,000 列（3 次中位數）",
        value: Math.round(median),
        threshold: 2000,
        unit: "ms",
        pass: median < 2000,
        note: `樣本 ${samples.map((v) => Math.round(v)).join("、")}ms`,
      });
      expect.soft(median, `10,000 列的 LCP 中位數 ${median}ms`).toBeLessThan(2000);
    }
    const ctx = await offlineContext(browser, served!.base, storage);
    const page = await ctx.newPage();
    await page.goto("/library?group=none");
    await waitReady(page, "媒體庫");
    const total = Number(await page.locator("[aria-setsize]").first().getAttribute("aria-setsize"));
    expect(total).toBe(10_000);
    const r = await scrollToBottom(page, total, 12 * 60_000);
    const baseline =
      worstStreakOf(r.intervals) >= 3 ? await baselineScroll(page, total) : undefined;
    await ctx.close();
    judgeScroll(info, "捲動 10,000 列", total, r, baseline);
  });
});
