import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePageTitle } from "../shell/usePageTitle";

/** 找不到的內容（放在 AppShell 的 <main> 裡）。 */
export function NotFoundContent() {
  const { t } = useTranslation();
  usePageTitle(t("pages.notFound.title"));
  return (
    <>
      <h1 className="page__title">{t("pages.notFound.title")}</h1>
      <p>{t("pages.notFound.body")}</p>
      <Link to="/library">{t("pages.notFound.back")}</Link>
    </>
  );
}

/** 根路由的 404：沒有 AppShell，自己提供 <main>。 */
export function NotFoundPage() {
  return (
    <main className="main" id="main">
      <NotFoundContent />
    </main>
  );
}
