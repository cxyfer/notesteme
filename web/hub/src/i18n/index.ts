// Hub UI 的 i18n（同 Client：繁中（預設）、简中、English；沿用 web/client/src/i18n 的寫法）。
// 字典在 src/i18n/locales；新增字串時三種語言一起加，npm run check:i18n 會檢查 key 一致。
import i18next from "i18next";
import { initReactI18next } from "react-i18next";

import en from "./locales/en.json";
import zhHans from "./locales/zh-Hans.json";
import zhHant from "./locales/zh-Hant.json";

export const LANGUAGES = ["zh-Hant", "zh-Hans", "en"] as const;
export type Language = (typeof LANGUAGES)[number];
export const DEFAULT_LANGUAGE: Language = "zh-Hant";

const STORAGE_KEY = "notesteme-hub.lang";

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
export function formatNumber(n: number, lang: Language = currentLanguage()) {
  return new Intl.NumberFormat(lang).format(n);
}

export function formatDateTime(d: Date | string, lang: Language = currentLanguage()) {
  return new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" }).format(
    typeof d === "string" ? new Date(d) : d,
  );
}

/** 只有日期（媒體庫的發布日） */
export function formatDate(d: Date | string, lang: Language = currentLanguage()) {
  return new Intl.DateTimeFormat(lang, { dateStyle: "medium" }).format(
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
