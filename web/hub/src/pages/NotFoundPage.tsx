import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePageTitle } from "../shell/usePageTitle";

export function NotFoundPage() {
  const { t } = useTranslation();
  usePageTitle(t("notFound.title"));
  return (
    <section>
      <h1 className="page__title">{t("notFound.title")}</h1>
      <p>{t("notFound.body")}</p>
      <Link to="/">{t("notFound.back")}</Link>
    </section>
  );
}
