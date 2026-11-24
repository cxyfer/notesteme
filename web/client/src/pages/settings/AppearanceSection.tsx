// 外觀（pages.md §12）：淺色、深色或跟隨系統。和導覽底部的快速切換共用同一個值（useTheme）。
// 介面密度：舒適（預設）或緊湊（design-language.md §5.5），和主題一樣存在這個瀏覽器。
import { useTranslation } from "react-i18next";

import { DENSITIES, saveDensity } from "../../theme/density";
import { saveTheme, THEMES } from "../../theme/theme";
import { useDensity } from "../../theme/useDensity";
import { useTheme } from "../../theme/useTheme";
import { Panel } from "./shared";

const LABELS = { system: "prefs.themeSystem", light: "prefs.themeLight", dark: "prefs.themeDark" };

export function AppearanceSection() {
  const { t } = useTranslation();
  const theme = useTheme();
  const density = useDensity();
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
      <Panel
        title={t("settings.appearance.densityTitle")}
        description={t("settings.appearance.densityHint")}
      >
        <fieldset className="settings-fieldset">
          <legend className="visually-hidden">{t("settings.appearance.densityTitle")}</legend>
          {DENSITIES.map((d) => (
            <label key={d} className="settings-check">
              <input
                type="radio"
                name="density"
                value={d}
                checked={density === d}
                onChange={() => saveDensity(d)}
              />
              {t(`settings.appearance.density.${d}`)}
            </label>
          ))}
        </fieldset>
      </Panel>
    </>
  );
}
