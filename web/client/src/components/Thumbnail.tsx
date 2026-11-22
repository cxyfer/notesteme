// 縮圖（媒體庫的列、新增抽屜的預覽）。CSP 的 img-src 只允許同源、*.ytimg.com 與 *.hdslb.com：
// YouTube 的縮圖可以從影片 ID 推得；其他平台（後端還沒有縮圖網址）與載入失敗時顯示樣式化的占位。
import { useState } from "react";

const YT_ID = /^[A-Za-z0-9_-]{11}$/;
const ALLOWED_HOSTS = [/(^|\.)ytimg\.com$/, /(^|\.)hdslb\.com$/];

/** YouTube 影片 ID → 縮圖網址；ID 格式不對時回傳 undefined */
export function thumbnailUrl(platform: string, externalId: string): string | undefined {
  if (platform === "youtube" && YT_ID.test(externalId)) {
    return `https://i.ytimg.com/vi/${externalId}/mqdefault.jpg`;
  }
  return undefined;
}

/** 只接受 CSP 允許的 https 網域，其他一律不載入 */
export function allowedImageUrl(src: string | undefined): string | undefined {
  if (!src) return undefined;
  try {
    const u = new URL(src);
    if (u.protocol !== "https:") return undefined;
    return ALLOWED_HOSTS.some((re) => re.test(u.hostname)) ? u.href : undefined;
  } catch {
    return undefined;
  }
}

export interface ThumbnailProps {
  platform: string;
  externalId?: string;
  /** 明確給定的縮圖網址（例如之後後端提供的 Bilibili 縮圖）；優先於由 ID 推得的網址 */
  src?: string;
  /** 占位上顯示的平台名稱（呼叫端翻譯好） */
  placeholder: string;
  className?: string;
}

/** 縮圖是輔助資訊：alt 為空（標題就在旁邊），不送 Referer。 */
export function Thumbnail({ platform, externalId, src, placeholder, className }: ThumbnailProps) {
  const url = allowedImageUrl(src) ?? thumbnailUrl(platform, externalId ?? "");
  const [failed, setFailed] = useState<string | undefined>();
  const cls = ["thumb", className].filter(Boolean).join(" ");
  if (!url || failed === url) {
    return (
      <span className={`${cls} thumb--placeholder`} data-platform={platform} aria-hidden="true">
        {placeholder}
      </span>
    );
  }
  return (
    <img
      className={cls}
      src={url}
      alt=""
      loading="lazy"
      decoding="async"
      referrerPolicy="no-referrer"
      onError={() => setFailed(url)}
    />
  );
}
