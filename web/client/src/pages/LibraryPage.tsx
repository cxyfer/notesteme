import { useTranslation } from "react-i18next";

import { PlaceholderPage } from "./PlaceholderPage";

// UI-1：媒體庫與新增抽屜。
export function LibraryPage() {
  const { t } = useTranslation();
  return <PlaceholderPage title={t("pages.library.title")} />;
}
