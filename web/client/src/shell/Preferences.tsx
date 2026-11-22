// 主題與介面語言（暫時放在導覽底部；UI-3 的「外觀」「語言」設定分區完成後可以移過去）。
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { currentLanguage, LANGUAGE_NAMES, LANGUAGES, setLanguage, type Language } from "../i18n";
import { readTheme, saveTheme, THEMES, type Theme } from "../theme/theme";

const THEME_LABELS: Record<Theme, string> = {
  system: "prefs.themeSystem",
  light: "prefs.themeLight",
  dark: "prefs.themeDark",
};

export function Preferences() {
  const { t, i18n } = useTranslation();
  const [theme, setTheme] = useState<Theme>(readTheme);
  const lang = currentLanguage();
  void i18n.language; // 語言改變時重新渲染

  return (
    <div className="prefs">
      <label className="prefs">
        {t("prefs.theme")}
        <select
          value={theme}
          onChange={(e) => {
            const v = e.target.value as Theme;
            setTheme(v);
            saveTheme(v);
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
