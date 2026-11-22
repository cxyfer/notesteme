import { getRouteApi, Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePageTitle } from "../shell/usePageTitle";
import { NotFoundContent } from "./NotFoundPage";

// M1 的設定分區（docs/ui/pages.md §12）；UI-3 實作各分區的表單，M2 之後的分區再加進來。
export const SETTINGS_SECTIONS = [
  "services",
  "accounts",
  "language",
  "system",
  "appearance",
  "login",
] as const;
type Section = (typeof SETTINGS_SECTIONS)[number];

const route = getRouteApi("/app/settings/$section");

export function SettingsPage() {
  const { t } = useTranslation();
  const { section } = route.useParams();
  const known = (SETTINGS_SECTIONS as readonly string[]).includes(section);
  usePageTitle(
    known
      ? `${t(`pages.settings.${section as Section}`)} · ${t("pages.settings.title")}`
      : undefined,
  );
  if (!known) return <NotFoundContent />;
  return (
    <>
      <h1 className="page__title">{t("pages.settings.title")}</h1>
      <div className="settings">
        <nav aria-label={t("pages.settings.sections")}>
          <ul className="nav__list">
            {SETTINGS_SECTIONS.map((s) => (
              <li key={s}>
                <Link
                  className="nav__item"
                  to="/settings/$section"
                  params={{ section: s }}
                  aria-current={s === section ? "page" : undefined}
                >
                  {t(`pages.settings.${s}`)}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
        <section aria-labelledby="settings-section-title">
          <h2 id="settings-section-title">{t(`pages.settings.${section as Section}`)}</h2>
          <p className="page__empty">{t("pages.notImplemented")}</p>
        </section>
      </div>
    </>
  );
}
