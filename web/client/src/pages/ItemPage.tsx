import { getRouteApi } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { PlaceholderPage } from "./PlaceholderPage";

const route = getRouteApi("/app/items/$id");

// UI-2：影音詳情。
export function ItemPage() {
  const { t } = useTranslation();
  const { id } = route.useParams();
  return (
    <PlaceholderPage title={t("pages.item.title")}>
      <p className="secondary">{t("pages.item.id", { id })}</p>
    </PlaceholderPage>
  );
}
