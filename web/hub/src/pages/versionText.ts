// 版本的顯示文字：影片代號、種類／變體／語言、模型欄位（HREP-R7）。
import type { TFunction } from "i18next";

import type { VideoRef } from "../api/types";

export function videoLabel(v: VideoRef): string {
  return `${v.platform}:${v.external_id}${v.part ? `/p${v.part}` : ""}`;
}

/** 影片頁的路由參數 */
export function videoLink(v: VideoRef) {
  return {
    to: "/videos/$platform/$externalId" as const,
    params: { platform: v.platform, externalId: v.external_id },
    search: v.part ? { part: v.part } : {},
  };
}

export function kindLabel(t: TFunction, kind: string, variant: string, lang: string): string {
  const parts = [t(`kind.${kind}`, { defaultValue: kind })];
  if (variant) parts.push(t(`variant.${variant}`, { defaultValue: variant }));
  if (lang) parts.push(lang);
  return parts.join(" · ");
}
