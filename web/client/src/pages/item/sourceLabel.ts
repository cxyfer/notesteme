// 字幕來源 ID（docs/specs/steps.md 第 3 節）的人話名稱（pages.md §4 的「字幕來源 YouTube 人工字幕」）。
// ASR 的 ID 是設定檔自訂的，不認得的 ID 原樣顯示。
import type { TFunction } from "i18next";

export function sourceLabel(t: TFunction, code: string): string {
  return t(`item.source.${code}`, { defaultValue: code });
}
