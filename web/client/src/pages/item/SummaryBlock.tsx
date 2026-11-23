// 影音詳情的摘要（DIG-R5）與摘要後暫停（DIG-R6、R7）：摘要、標題譯文、重點；
// 有 awaiting_user 的步驟時顯示「繼續處理」與「取消」。摘要是 LLM 的輸出（不可信）：一律當純文字。
import { useTranslation } from "react-i18next";

import type { components } from "../../api/schema";
import { Button } from "../../components";

type Summary = components["schemas"]["ItemSummary"];

export function SummaryBlock({
  summary,
  awaiting,
  busy,
  onContinue,
  onCancel,
}: {
  summary?: Summary;
  /** 停在 awaiting_user 的步驟名稱；沒有暫停時是 undefined */
  awaiting?: string;
  busy: boolean;
  onContinue: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  if (!summary && !awaiting) return null;
  const points = summary?.key_points ?? [];
  return (
    <section className="item-summary" aria-labelledby="item-summary-title">
      <h2 id="item-summary-title" className="item-summary__title">
        {t("item.summary.title")}
      </h2>
      {summary ? (
        <div lang={summary.language || undefined}>
          {summary.title_translation && (
            <p className="item-summary__translation">{summary.title_translation}</p>
          )}
          <p className="item-summary__text">{summary.text}</p>
          {points.length > 0 && (
            <ul className="item-summary__points">
              {points.map((p, i) => (
                <li key={i}>{p}</li>
              ))}
            </ul>
          )}
        </div>
      ) : (
        <p className="secondary">{t("item.summary.none")}</p>
      )}
      {awaiting && (
        <div className="item-summary__pause" role="group" aria-label={t("item.summary.paused")}>
          <p>{t("item.summary.paused")}</p>
          <div className="item-summary__actions">
            <Button variant="primary" disabled={busy} onClick={onContinue}>
              {t("actions.continue")}
            </Button>
            <Button variant="text" disabled={busy} onClick={onCancel}>
              {t("actions.cancelItem")}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
