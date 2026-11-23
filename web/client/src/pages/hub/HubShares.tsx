// 影音詳情的「已分享到 Hub」（HSHR-R2 的 hub_shares）：每次分享的時間與 Hub 上「我的分享」的連結。
// 撤回沒有 Client 路由（HSHR S12），要到 Hub 網頁進行。
import { useTranslation } from "react-i18next";

import type { components } from "../../api/schema";
import { formatDateTime } from "../../i18n";
import "../../styles/hub.css";
import { hubLink } from "./hub";

type Share = components["schemas"]["HubShareRef"];

/** Hub 上「我的分享」所在的頁面 */
const ACCOUNT_PATH = "/account";

export function HubShares({ shares }: { shares: readonly Share[] | null | undefined }) {
  const { t } = useTranslation();
  if (!shares?.length) return null;
  return (
    <section className="hub-shares" aria-label={t("hub.shared")}>
      <h2 className="hub-shares__title">{t("hub.shared")}</h2>
      <ul>
        {shares.map((s) => {
          const href = hubLink(s.hub_url, ACCOUNT_PATH);
          return (
            <li key={s.share_id}>
              {formatDateTime(s.created_at)}{" "}
              {href && (
                <a href={href} target="_blank" rel="noopener noreferrer">
                  {t("hub.manageOnHub")}
                </a>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
