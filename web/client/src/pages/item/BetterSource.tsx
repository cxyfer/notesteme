// 更好的字幕來源（SRC-R18～R23）：GET /api/items/{id} 的 better_source 與 reprobe。
//   - better_source：提示與「改用更好的字幕重跑」（= 從 transcript 重新處理，開啟既有的重新處理對話框）。
//     estimate_usd 是 null 時寫「無法預估費用」，不顯示 $0.00（同 UI-4 決定 4）。
//   - reprobe：之後還會重新檢查字幕來源與 SponsorBlock 的時間。
import { useTranslation } from "react-i18next";

import type { components } from "../../api/schema";
import { Button } from "../../components";
import { formatDateTime, formatUsd } from "../../i18n";

type Better = components["schemas"]["ItemBetterSource"];
type Reprobe = components["schemas"]["ItemReprobe"];

export function BetterSource({
  better,
  reprobe,
  onRerun,
}: {
  better: Better | null;
  reprobe: Reprobe | null;
  onRerun: () => void;
}) {
  const { t } = useTranslation();
  return (
    <>
      {better && (
        <section className="item-notice" aria-labelledby="item-better-title">
          <h2 id="item-better-title" className="item-notice__title">
            {t("item.better.title")}
          </h2>
          <p>
            {t("item.better.found", {
              time: formatDateTime(better.found_at),
              lang: better.lang,
              kind: t(`settings.language.kind.${better.kind}`, { defaultValue: better.kind }),
              source: better.source,
            })}{" "}
            {better.estimate_usd == null
              ? t("item.better.noEstimate")
              : t("item.better.estimate", { usd: formatUsd(better.estimate_usd) })}
          </p>
          <div className="item-notice__actions">
            <Button variant="primary" onClick={onRerun}>
              {t("actions.rerunBetter")}
            </Button>
          </div>
        </section>
      )}
      {reprobe && (
        <p className="secondary item-reprobe">
          {t("item.better.reprobe", {
            what:
              reprobe.sources && reprobe.sponsorblock
                ? t("item.better.what.both")
                : reprobe.sponsorblock
                  ? t("item.better.what.sponsorblock")
                  : t("item.better.what.sources"),
            next: formatDateTime(reprobe.next_at),
            until: formatDateTime(reprobe.until),
          })}
        </p>
      )}
    </>
  );
}
