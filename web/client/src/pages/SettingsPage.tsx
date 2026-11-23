// 設定（docs/ui/pages.md §12）：左側分區清單、右側表單，每個分區各自儲存。
// 窄螢幕（< 768px）時清單收成頂部的下拉選單。未知的分區導到第一個分區（UI-3b 決定）。
import { getRouteApi, Link, Navigate, useNavigate } from "@tanstack/react-router";
import { lazy, Suspense, type ComponentType } from "react";
import { useTranslation } from "react-i18next";

import "../styles/settings.css";
import { usePageTitle } from "../shell/usePageTitle";
import { AccountsSection } from "./settings/AccountsSection";
import { AgentSection } from "./settings/AgentSection";
import { AppearanceSection } from "./settings/AppearanceSection";
import { BookmarkletSection } from "./settings/BookmarkletSection";
import { BudgetSection } from "./settings/BudgetSection";
import { ConfigSection } from "./settings/ConfigSection";
import { GlossarySection } from "./settings/GlossarySection";
import { LanguageSection } from "./settings/LanguageSection";
import { LoginSection } from "./settings/LoginSection";
import { NotificationsSection } from "./settings/NotificationsSection";
import { OutputsSection } from "./settings/OutputsSection";
import { ServicesSection } from "./settings/ServicesSection";
import { SystemSection } from "./settings/SystemSection";
import { Loading } from "./settings/shared";

// M5-S9：Hub 分區（HSHR）lazy 載入，不算進初始 bundle
const HubSectionLazy = lazy(() =>
  import("./hub/HubSection").then((m) => ({ default: m.HubSection })),
);
function HubSection() {
  return (
    <Suspense fallback={<Loading />}>
      <HubSectionLazy />
    </Suspense>
  );
}

// 分區的順序依 pages.md §12；「匯出與匯入」是 UI-3b 加的獨立分區（SEC-R10），「書籤小工具」是 ENTRY-R1 的區塊。
export const SETTINGS_SECTIONS = [
  "services",
  "accounts",
  "language",
  "outputs",
  "agent",
  "notifications",
  "budget",
  "glossary",
  "system",
  "hub",
  "config",
  "appearance",
  "bookmarklet",
  "login",
] as const;
export type SettingsSection = (typeof SETTINGS_SECTIONS)[number];

const SECTION_COMPONENTS: Record<SettingsSection, ComponentType> = {
  services: ServicesSection,
  accounts: AccountsSection,
  language: LanguageSection,
  outputs: OutputsSection,
  agent: AgentSection,
  notifications: NotificationsSection,
  budget: BudgetSection,
  glossary: GlossarySection,
  system: SystemSection,
  hub: HubSection,
  config: ConfigSection,
  appearance: AppearanceSection,
  bookmarklet: BookmarkletSection,
  login: LoginSection,
};

const route = getRouteApi("/app/settings/$section");

function isSection(s: string): s is SettingsSection {
  return (SETTINGS_SECTIONS as readonly string[]).includes(s);
}

export function SettingsPage() {
  const { t } = useTranslation();
  const { section } = route.useParams();
  const navigate = useNavigate();
  const known = isSection(section);
  usePageTitle(
    known ? `${t(`settings.sections.${section}`)} · ${t("pages.settings.title")}` : undefined,
  );
  if (!known) {
    return <Navigate to="/settings/$section" params={{ section: SETTINGS_SECTIONS[0] }} replace />;
  }
  const Body = SECTION_COMPONENTS[section];
  return (
    <>
      <h1 className="page__title">{t("pages.settings.title")}</h1>
      <div className="settings">
        <nav className="settings__nav" aria-label={t("pages.settings.sections")}>
          <ul className="nav__list">
            {SETTINGS_SECTIONS.map((s) => (
              <li key={s}>
                <Link
                  className="nav__item"
                  to="/settings/$section"
                  params={{ section: s }}
                  aria-current={s === section ? "page" : undefined}
                >
                  {t(`settings.sections.${s}`)}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
        <label className="settings__menu">
          <span>{t("pages.settings.sections")}</span>
          <select
            value={section}
            onChange={(e) =>
              void navigate({ to: "/settings/$section", params: { section: e.target.value } })
            }
          >
            {SETTINGS_SECTIONS.map((s) => (
              <option key={s} value={s}>
                {t(`settings.sections.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <section className="settings__body" aria-labelledby="settings-section-title">
          <h2 id="settings-section-title">{t(`settings.sections.${section}`)}</h2>
          {/* key：換分區時重設表單的本地狀態（未儲存的輸入、剛建立的 token） */}
          <Body key={section} />
        </section>
      </div>
    </>
  );
}
