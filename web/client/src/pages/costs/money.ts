// 金額的顯示（COST 規格「金額的計算與寫法」）：API 的金額是 micro-USD ÷ 1e6 的數字，前端不自己加總，
// 只顯示伺服器算好的值。給人看的總額用 2 位小數（$0.04、$20.00）；明細的單筆金額常小於 1 美分，
// 沿用 formatUsd（小於 $0.01 時最多 4 位），單價最多 6 位。
import { currentLanguage, type Language } from "../../i18n";

export function formatUsdCents(n: number, lang: Language = currentLanguage()) {
  return new Intl.NumberFormat(lang, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(n);
}

export function formatUnitPrice(n: number, lang: Language = currentLanguage()) {
  return new Intl.NumberFormat(lang, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  }).format(n);
}
