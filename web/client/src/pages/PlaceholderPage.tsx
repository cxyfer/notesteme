import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { usePageTitle } from "../shell/usePageTitle";

/** 尚未實作的頁面：標題加「還在製作中」的說明（UI-1～3 會換成真正的內容）。 */
export function PlaceholderPage({ title, children }: { title: string; children?: ReactNode }) {
  const { t } = useTranslation();
  usePageTitle(title);
  return (
    <>
      <h1 className="page__title">{title}</h1>
      {children}
      <p className="page__empty">{t("pages.notImplemented")}</p>
    </>
  );
}
