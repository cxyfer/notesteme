// Hub 相關的小工具（HSHR）：版本 ID 的解析、Hub 網址的組合。

const ID = /^[A-Za-z0-9_-]{1,64}$/;

/** 接受版本 ID，或 Hub 版本頁的網址（…/versions/<id>）；不合法時回 undefined。 */
export function parseVersionRef(input: string): string | undefined {
  const s = input.trim();
  if (ID.test(s)) return s;
  const m = /\/versions\/([A-Za-z0-9_-]{1,64})\/?(?:[?#].*)?$/.exec(s);
  return m ? m[1] : undefined;
}

/** Hub 上的頁面網址；hub_url 不是 https 時不產生連結（Hub 回來的值也當作不可信）。 */
export function hubLink(hubUrl: string | undefined, path: string): string | undefined {
  if (!hubUrl) return undefined;
  try {
    const u = new URL(hubUrl);
    if (u.protocol !== "https:") return undefined;
    return `${u.origin}${u.pathname.replace(/\/$/, "")}${path}`;
  } catch {
    return undefined;
  }
}
