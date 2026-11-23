// 書籤小工具與分享帶進來的網址（ENTRY-R1、R2、R4）。網址來自第三方頁面與其他 App，一律當不可信輸入。
import { isHttpUrl } from "./url";

/** 帶進來的網址上限，以字元（code point）計；同後端的 entry.MaxURLChars。 */
export const MAX_ENTRY_URL_CHARS = 2048;

/** /add 的 query（ENTRY-R2、R4）。 */
export interface AddEntrySearch {
  url?: string;
  share_error?: string;
}

export function validateAddEntrySearch(search: Record<string, unknown>): AddEntrySearch {
  const out: AddEntrySearch = {};
  // TanStack Router 會把看起來像 JSON 的值解析成數字等；只接受字串。
  if (typeof search.url === "string") out.url = search.url;
  if (typeof search.share_error === "string") out.share_error = search.share_error;
  return out;
}

export type EntryNotice = "invalidUrl" | "noUrl";

/** 決定要帶進「新增」抽屜的網址與提示：網址不是 http／https 或太長時忽略（輸入框留空）並提示。 */
export function resolveAddEntry(search: AddEntrySearch): { url?: string; notice?: EntryNotice } {
  if (search.url !== undefined) {
    const u = search.url.trim();
    if (isHttpUrl(u) && [...u].length <= MAX_ENTRY_URL_CHARS) return { url: u };
    return { notice: "invalidUrl" };
  }
  if (search.share_error === "no_url") return { notice: "noUrl" };
  return {};
}

/**
 * 書籤小工具的程式碼（ENTRY-R1）：只讀 location.href，在新分頁開啟 <origin>/add?url=…。
 * 不含任何憑證，不讀第三方頁面的 DOM，不在對方的頁面發出請求或插入元素。
 */
export function bookmarkletCode(origin: string): string {
  return `javascript:(()=>{window.open('${origin}/add?url='+encodeURIComponent(location.href),'_blank','noopener')})()`;
}
