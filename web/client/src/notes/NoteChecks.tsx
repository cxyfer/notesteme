// 筆記的自動檢查結果（NOTE-R7）：通過與否用文字與符號區分，不只靠顏色。
// 模板預覽與影音詳情的筆記分頁共用。
import { useTranslation } from "react-i18next";

import type { components } from "../api/schema";

type Check = components["schemas"]["NoteCheck"];

// 形狀符號（不是文字，螢幕閱讀器略過）
const MARK = { ok: "\u2713", failed: "\u2715" } as const;

export function NoteChecks({ checks }: { checks: readonly Check[] }) {
  const { t } = useTranslation();
  if (checks.length === 0) return <p className="secondary">{t("noteTemplates.noChecks")}</p>;
  return (
    <ul className="note-checks" aria-label={t("noteTemplates.checksResult")}>
      {checks.map((c) => (
        <li key={c.id} data-ok={c.ok}>
          <span aria-hidden="true">{c.ok ? MARK.ok : MARK.failed}</span>
          <span>
            {t(c.ok ? "noteTemplates.checkOk" : "noteTemplates.checkFailed", {
              name: t(`noteTemplates.check.${c.id}`, { defaultValue: c.id }),
            })}
            {!c.ok && c.message ? t("subs.errorSep") + c.message : ""}
          </span>
        </li>
      ))}
    </ul>
  );
}
