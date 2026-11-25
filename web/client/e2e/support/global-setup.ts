// 所有 project（引擎）共用一個 serve：資料只產生一次，session cookie 不分引擎。
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";

import { apiCall, login, outDir, startServe } from "./server";

export interface E2EEnv {
  base: string;
  /** 有繁中筆記、摘要、章節、畫面的影音（原文 zh-Hant） */
  noteItem: string;
  noteItemRef: string;
  /** 原文英文、有章節與成本的影音 */
  enItem: string;
  enItemRef: string;
  /** segment 失敗（「需要你處理」）的影音 */
  failedItems: string[];
  subscription: string;
  /** 放進垃圾桶的影音標題 */
  trashedTitle: string;
}

interface ItemRow {
  id: string;
  ref: string;
  title: string;
}

export default async function globalSetup() {
  const port = Number(process.env.NOTESTEME_E2E_PORT ?? 18181);
  const served = await startServe({
    port,
    // 200 部、10 個頻道、每部 600 句：第 5、102、199 部的 segment 失敗（benchdata 每 97 部一部），
    // 「需要你處理」有三部：RWD-1 處理掉其中一部之後，其他頁面仍有內容可以檢查。
    data: { scale: 0.02, seed: true },
    dataDir: process.env.NOTESTEME_E2E_DATA,
  });
  const state = await login(served.base);
  mkdirSync(path.join(outDir, "state"), { recursive: true });
  writeFileSync(path.join(outDir, "state", "session.json"), JSON.stringify(state));

  const list = (await apiCall(served.base, state, "GET", "/api/items?limit=200")) as {
    items: ItemRow[];
  };
  const byId = (id: string) => list.items.find((i) => i.id === id);
  const seed = served.seed!;
  const ov = (await apiCall(served.base, state, "GET", "/api/overview")) as {
    needs_you: { items: { id: string }[] };
  };
  const failed = ov.needs_you.items.map((i) => i.id);
  if (failed.length < 2)
    throw new Error("資料裡「需要你處理」少於兩部（benchdata 每 97 部有一部 segment 失敗）");
  // 垃圾桶：固定放第 42 部（沿用資料目錄時已經在垃圾桶裡，列表就找不到它，略過）。
  const trash = list.items.find((i) => i.title.startsWith("Video 42 "));
  if (trash) {
    await apiCall(served.base, state, "DELETE", `/api/items/${encodeURIComponent(trash.id)}`);
  }
  const env: E2EEnv = {
    base: served.base,
    noteItem: seed.note_item,
    noteItemRef: byId(seed.note_item)!.ref,
    enItem: seed.en_item,
    enItemRef: byId(seed.en_item)!.ref,
    failedItems: failed,
    subscription: seed.subscription,
    trashedTitle: "Video 42 ",
  };
  writeFileSync(path.join(outDir, "state", "env.json"), JSON.stringify(env, null, 2));

  return async () => {
    await served.stop();
  };
}
