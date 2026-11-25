// i18n（ADR-0008、NFR-I18N-1～3）：繁中（預設）、简中、English。
// 字典在 src/i18n/locales；新增字串時三種語言一起加，npm run check:i18n 會檢查 key 一致。
import i18next from "i18next";
import { initReactI18next } from "react-i18next";

import en from "./locales/en.json";
import zhHans from "./locales/zh-Hans.json";
import zhHant from "./locales/zh-Hant.json";

export const LANGUAGES = ["zh-Hant", "zh-Hans", "en"] as const;
export type Language = (typeof LANGUAGES)[number];
export const DEFAULT_LANGUAGE: Language = "zh-Hant";

const STORAGE_KEY = "notesteme.lang";

// 每種語言用自己的語言名稱顯示（不翻譯）。
export const LANGUAGE_NAMES: Record<Language, string> = {
  "zh-Hant": "繁體中文",
  "zh-Hans": "简体中文",
  en: "English",
};

/** 把 BCP 47 語言標籤對應到支援的 UI 語言；對應不到時回傳 undefined。 */
export function matchLanguage(tag: string): Language | undefined {
  const t = tag.toLowerCase();
  if (t === "en" || t.startsWith("en-")) return "en";
  if (!(t === "zh" || t.startsWith("zh-"))) return undefined;
  if (/^zh-(hans|cn|sg|my)\b/.test(t)) return "zh-Hans";
  return "zh-Hant";
}

/** 決定初始語言：使用者選過的 > 瀏覽器偏好 > 繁中。 */
export function detectLanguage(
  stored: string | null = readStored(),
  preferred: readonly string[] = typeof navigator === "undefined" ? [] : navigator.languages,
): Language {
  if (stored && (LANGUAGES as readonly string[]).includes(stored)) return stored as Language;
  for (const tag of preferred) {
    const m = matchLanguage(tag);
    if (m) return m;
  }
  return DEFAULT_LANGUAGE;
}

function readStored(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export function setLanguage(lang: Language): Promise<unknown> {
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // localStorage 不可用（隱私模式）時只在這次工作階段生效
  }
  return i18next.changeLanguage(lang);
}

export function currentLanguage(): Language {
  const l = i18next.resolvedLanguage ?? i18next.language;
  return (LANGUAGES as readonly string[]).includes(l) ? (l as Language) : DEFAULT_LANGUAGE;
}

// <html lang> 跟隨 UI 語言：CSS 的 :lang() 依它切換中文字型堆疊（design-language.md §4.2）。
function syncDocumentLang(lang: string) {
  if (typeof document !== "undefined") document.documentElement.lang = lang;
}

export function initI18n(lang: Language = detectLanguage()) {
  if (!i18next.isInitialized) {
    void i18next.use(initReactI18next).init({
      resources: {
        "zh-Hant": { translation: zhHant },
        "zh-Hans": { translation: zhHans },
        en: { translation: en },
      },
      lng: lang,
      fallbackLng: DEFAULT_LANGUAGE,
      supportedLngs: [...LANGUAGES],
      interpolation: { escapeValue: false }, // React 已經會跳脫
      returnNull: false,
    });
    i18next.on("languageChanged", syncDocumentLang);
  }
  syncDocumentLang(i18next.language);
  return i18next;
}

// 日期與數字一律用 Intl（NFR-I18N-3）；時間碼不隨語言改變，見 formatTimecode。
//
// Intl 的格式器建立很貴（Chromium 每個約 0.1ms 以上）：媒體庫捲動時每一列都會格式化日期，
// 每次新建會讓 10,000 列的捲動出現長工作（NFR-PERF-10，docs/ui/README.md 10.32）。依語言與選項快取。
const formatters = new Map<
  string,
  Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat
>();

function cachedFormat<T extends Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat>(
  kind: string,
  lang: string,
  opts: object,
  make: () => T,
): T {
  const key = `${kind}|${lang}|${JSON.stringify(opts)}`;
  let f = formatters.get(key) as T | undefined;
  if (!f) {
    f = make();
    formatters.set(key, f);
  }
  return f;
}

export function numberFormat(lang: string, opts: Intl.NumberFormatOptions = {}) {
  return cachedFormat("n", lang, opts, () => new Intl.NumberFormat(lang, opts));
}

export function dateTimeFormat(lang: string, opts: Intl.DateTimeFormatOptions = {}) {
  return cachedFormat("d", lang, opts, () => new Intl.DateTimeFormat(lang, opts));
}

function relativeTimeFormat(lang: string, opts: Intl.RelativeTimeFormatOptions) {
  return cachedFormat("r", lang, opts, () => new Intl.RelativeTimeFormat(lang, opts));
}

export function formatNumber(n: number, lang: Language = currentLanguage()) {
  return numberFormat(lang).format(n);
}

export function formatDateTime(d: Date | string, lang: Language = currentLanguage()) {
  return dateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" }).format(
    typeof d === "string" ? new Date(d) : d,
  );
}

/**
 * 相對時間（「3 小時前」「昨天」）：依距離選秒、分鐘、小時、天、月、年，用 Intl.RelativeTimeFormat。
 * 未來的時間（時鐘不同步）也照實顯示，不硬改成「剛剛」。
 */
export function formatRelativeTime(
  d: Date | string,
  now: number = Date.now(),
  lang: Language = currentLanguage(),
) {
  const diffSec = Math.round(((typeof d === "string" ? new Date(d) : d).getTime() - now) / 1000);
  const abs = Math.abs(diffSec);
  const [value, unit]: [number, Intl.RelativeTimeFormatUnit] =
    abs < 60
      ? [diffSec, "second"]
      : abs < 3600
        ? [Math.round(diffSec / 60), "minute"]
        : abs < 86_400
          ? [Math.round(diffSec / 3600), "hour"]
          : abs < 30 * 86_400
            ? [Math.round(diffSec / 86_400), "day"]
            : abs < 365 * 86_400
              ? [Math.round(diffSec / (30 * 86_400)), "month"]
              : [Math.round(diffSec / (365 * 86_400)), "year"];
  return relativeTimeFormat(lang, { numeric: "auto" }).format(value, unit);
}

/** 只有日期（媒體庫的發布日） */
export function formatDate(d: Date | string, lang: Language = currentLanguage()) {
  return dateTimeFormat(lang, { dateStyle: "medium" }).format(
    typeof d === "string" ? new Date(d) : d,
  );
}

/** 時間碼：MM:SS 或 H:MM:SS，不隨語言改變（docs/ui/README.md §7）。 */
export function formatTimecode(totalSeconds: number) {
  const s = Math.max(0, Math.floor(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, "0");
  return h > 0
    ? `${h}:${String(m).padStart(2, "0")}:${sec}`
    : `${String(m).padStart(2, "0")}:${sec}`;
}

/** 位元組：依 UI 語言的 KB／MB／GB（1024 進位） */
export function formatBytes(n: number, lang: Language = currentLanguage()) {
  const units = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"] as const;
  let v = Math.max(0, n);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return numberFormat(lang, {
    style: "unit",
    unit: units[i],
    unitDisplay: "short",
    maximumFractionDigits: i === 0 ? 0 : 1,
  }).format(v);
}

/** 美元金額（成本一律以 USD 記錄） */
export function formatUsd(n: number, lang: Language = currentLanguage()) {
  return numberFormat(lang, {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: n !== 0 && Math.abs(n) < 0.01 ? 4 : 2,
  }).format(n);
}

/** 已經過多久：依長度選分鐘、小時或天，用 Intl 的單位格式（不另外加字串） */
export function formatElapsed(ms: number, lang: string = currentLanguage()): string {
  const min = Math.max(0, Math.floor(ms / 60_000));
  const [value, unit] =
    min < 60
      ? [min, "minute"]
      : min < 48 * 60
        ? [Math.floor(min / 60), "hour"]
        : [Math.floor(min / 1440), "day"];
  return numberFormat(lang, { style: "unit", unit, unitDisplay: "long" }).format(value);
}
