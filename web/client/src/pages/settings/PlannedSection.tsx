// 之後的里程碑才提供的分區：先列在清單裡，內容只說明，不放表單（UI-4 起通知、預算已有內容，目前沒有分區使用）。
import { useTranslation } from "react-i18next";

import { NotAvailable } from "./shared";

export function PlannedSection({ milestone }: { milestone: string }) {
  const { t } = useTranslation();
  return <NotAvailable>{t("settings.common.plannedFor", { milestone })}</NotAvailable>;
}
