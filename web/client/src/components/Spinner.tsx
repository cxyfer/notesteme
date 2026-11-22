import { useTranslation } from "react-i18next";

/** 載入中：有文字標籤（預設「載入中…」），reduced motion 時不轉動。 */
export function Spinner({ label, showLabel = false }: { label?: string; showLabel?: boolean }) {
  const { t } = useTranslation();
  const text = label ?? t("common.loading");
  return (
    <span className="spinner" role="status">
      <span className="spinner__ring" aria-hidden="true" />
      <span className={showLabel ? undefined : "visually-hidden"}>{text}</span>
    </span>
  );
}
