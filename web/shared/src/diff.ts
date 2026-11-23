// 逐字差異：以 code point 為單位的 LCS，不切開 UTF-16 surrogate（試跑結果的「差異」模式；UI-2 的影音詳情也需要）。
// 字幕句很短（通常 < 200 字），O(n·m) 的動態規劃足夠；太長時退化成整段刪除＋新增，避免卡住畫面。
// 與 UI-2 分支的 web/client/src/pages/item/diff.ts 形狀相同，合併時改成都從 @shared 匯入。

export type DiffPart = { type: "eq" | "del" | "ins"; text: string };

/** 超過這個格數（n·m）就不逐字比對 */
const MAX_CELLS = 4_000_000;

export function diffChars(before: string, after: string): DiffPart[] {
  if (before === after) return before ? [{ type: "eq", text: before }] : [];
  const a = Array.from(before);
  const b = Array.from(after);
  // 共同的前綴與後綴先拿掉，縮小表格
  let pre = 0;
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++;
  let suf = 0;
  while (
    suf < a.length - pre &&
    suf < b.length - pre &&
    a[a.length - 1 - suf] === b[b.length - 1 - suf]
  )
    suf++;
  const am = a.slice(pre, a.length - suf);
  const bm = b.slice(pre, b.length - suf);
  const parts: DiffPart[] = [];
  const push = (type: DiffPart["type"], ch: string) => {
    const last = parts.at(-1);
    if (last?.type === type) last.text += ch;
    else parts.push({ type, text: ch });
  };
  for (const ch of a.slice(0, pre)) push("eq", ch);
  const n = am.length;
  const m = bm.length;
  if (n * m > MAX_CELLS) {
    for (const ch of am) push("del", ch);
    for (const ch of bm) push("ins", ch);
  } else {
    // lcs[i][j]：am[i:] 與 bm[j:] 的最長共同子序列長度
    const w = m + 1;
    const lcs = new Uint32Array((n + 1) * w);
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i * w + j] =
          am[i] === bm[j]
            ? lcs[(i + 1) * w + j + 1]! + 1
            : Math.max(lcs[(i + 1) * w + j]!, lcs[i * w + j + 1]!);
      }
    }
    let i = 0;
    let j = 0;
    while (i < n && j < m) {
      if (am[i] === bm[j]) {
        push("eq", am[i]!);
        i++;
        j++;
      } else if (lcs[(i + 1) * w + j]! >= lcs[i * w + j + 1]!) {
        push("del", am[i]!);
        i++;
      } else {
        push("ins", bm[j]!);
        j++;
      }
    }
    for (; i < n; i++) push("del", am[i]!);
    for (; j < m; j++) push("ins", bm[j]!);
  }
  for (const ch of a.slice(a.length - suf)) push("eq", ch);
  return parts;
}

/** 有沒有任何差異 */
export function hasChanges(parts: readonly DiffPart[]): boolean {
  return parts.some((p) => p.type !== "eq");
}
