// 影音詳情「字幕來源」的 Hub 標示（HSHR 介面的「來自 Hub：@alice 的原始字幕」，S23 的 hub_origin）。
//
//   - uploader 是 Hub 上的顯示名稱，不可信：只當文字節點渲染，過長時截斷，完整名稱放在 title。
//   - 版本頁的連結沿用 hubLink：只為 https 的 Hub 產生（同「已分享到 Hub」），另開分頁並帶 noopener noreferrer。
import { useTranslation } from "react-i18next";

import type { components } from "../../api/schema";
import { hubLink } from "./hub";

type Origin = components["schemas"]["ItemHubOrigin"];

/** 上傳者名稱最多顯示幾個字（以 code point 計，不切開 surrogate pair） */
export const UPLOADER_MAX = 32;

export function truncateName(name: string, max = UPLOADER_MAX): string {
  const cps = Array.from(name);
  return cps.length > max ? `${cps.slice(0, max).join("")}…` : name;
}

export function HubOrigin({ origin }: { origin: Origin | null | undefined }) {
  const { t } = useTranslation();
  if (!origin) return <>{t("hub.fromHub")}</>;
  const name = truncateName(origin.uploader);
  const href = origin.version_id
    ? hubLink(origin.url, `/versions/${encodeURIComponent(origin.version_id)}`)
    : undefined;
  return (
    <>
      <span title={name !== origin.uploader ? origin.uploader : undefined}>
        {name ? t("hub.fromHubBy", { uploader: name }) : t("hub.fromHub")}
      </span>
      {href && (
        <>
          {" "}
          <a href={href} target="_blank" rel="noopener noreferrer">
            {t("hub.viewVersion")}
          </a>
        </>
      )}
    </>
  );
}
