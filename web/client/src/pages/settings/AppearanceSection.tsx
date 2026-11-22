// 外觀（pages.md §12）：淺色、深色或跟隨系統。和導覽底部的快速切換共用同一個值（useTheme）。
// 介面密度沒有設計規格（design-language.md 沒有定義密度的 token），先標示尚未提供。
import { useTranslation } from "react-i18next";

import { saveTheme, THEMES } from "../../theme/theme";
import { useTheme } from "../../theme/useTheme";
import { NotAvailable, Panel } from "./shared";

const LABELS = { system: "prefs.themeSystem", light: "prefs.themeLight", dark: "prefs.themeDark" };

export function AppearanceSection() {
  const { t } = useTranslation();
  const theme = useTheme();
  return (
    <>
      <Panel title={t("prefs.theme")} description={t("settings.appearance.themeHint")}>
        <fieldset className="settings-fieldset">
          <legend className="visually-hidden">{t("prefs.theme")}</legend>
          {THEMES.map((th) => (
            <label key={th} className="settings-check">
              <input
                type="radio"
                name="theme"
                value={th}
                checked={theme === th}
                onChange={() => saveTheme(th)}
              />
              {t(LABELS[th])}
            </label>
          ))}
        </fieldset>
      </Panel>
      <Panel title={t("settings.appearance.densityTitle")}>
        <NotAvailable>{t("settings.appearance.densityUnavailable")}</NotAvailable>
      </Panel>
    </>
  );
}
