import { useTranslation } from "react-i18next";

import { PlaceholderPage } from "./PlaceholderPage";

// UI-2：佇列。
export function QueuePage() {
  const { t } = useTranslation();
  return <PlaceholderPage title={t("pages.queue.title")} />;
}
