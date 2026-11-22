// 主題與介面語言的快速切換（導覽底部）。設定頁的「外觀」「語言」分區有同樣的選項，兩邊共用同一個值（UI-3b 決定保留這裡的入口）。
import { useTranslation } from "react-i18next";

import { currentLanguage, LANGUAGE_NAMES, LANGUAGES, setLanguage, type Language } from "../i18n";
import { saveTheme, THEMES, type Theme } from "../theme/theme";
import { useTheme } from "../theme/useTheme";

const THEME_LABELS: Record<Theme, string> = {
  system: "prefs.themeSystem",
  light: "prefs.themeLight",
  dark: "prefs.themeDark",
};

export function Preferences() {
  const { t, i18n } = useTranslation();
  const theme = useTheme();
  const lang = currentLanguage();
  void i18n.language; // 語言改變時重新渲染

  return (
    <div className="prefs">
      <label className="prefs">
        {t("prefs.theme")}
        <select
          value={theme}
          onChange={(e) => {
            saveTheme(e.target.value as Theme);
          }}
        >
          {THEMES.map((th) => (
            <option key={th} value={th}>
              {t(THEME_LABELS[th])}
            </option>
          ))}
        </select>
      </label>
      <label className="prefs">
        {t("prefs.language")}
        <select value={lang} onChange={(e) => void setLanguage(e.target.value as Language)}>
          {LANGUAGES.map((l) => (
            <option key={l} value={l} lang={l}>
              {LANGUAGE_NAMES[l]}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}
