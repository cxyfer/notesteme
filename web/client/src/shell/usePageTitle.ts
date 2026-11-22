import { useEffect } from "react";
import { useTranslation } from "react-i18next";

/** document.title：「頁面 · notesteme」。 */
export function usePageTitle(title: string | undefined) {
  const { t } = useTranslation();
  const app = t("app.name");
  useEffect(() => {
    document.title = title ? `${title} · ${app}` : app;
  }, [title, app]);
}
