// 媒體庫的「格」檢視（pages.md §3：印樣式的縮圖網格，瀏覽畫面用，不是預設；UI-8）。lazy 載入。
// 只用列表已載入的資料（不像列檢視每列再讀 detail），不虛擬化：一頁 100 部，用「載入更多」接下一頁。
// 格檢視不提供多選與批次操作（要選取時切回列檢視）。
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { LoadMore, Thumbnail, type LoadMoreProps } from "../../components";
import { formatDate, formatTimecode } from "../../i18n";
import type { ApiItem } from "./stageView";
import "../../styles/ui8.css";

export function LibraryGrid({ items, more }: { items: readonly ApiItem[]; more: LoadMoreProps }) {
  const { t } = useTranslation();
  return (
    <>
      <ul className="lgrid" aria-label={t("library.listLabel")}>
        {items.map((it) => (
          <li key={it.id} className="lgrid__cell">
            <Thumbnail
              className="lgrid__thumb"
              platform={it.platform}
              externalId={it.external_id}
              placeholder={t(`platform.${it.platform}`, { defaultValue: it.platform })}
            />
            <Link
              to="/items/$id"
              params={{ id: it.id }}
              className="lgrid__title"
              lang={it.original_lang || undefined}
            >
              {it.title || t("library.untitled")}
            </Link>
            <p className="lgrid__meta secondary num">
              {it.duration_s != null && <span>{formatTimecode(it.duration_s)}</span>}
              {it.published_at && <span>{formatDate(it.published_at)}</span>}
            </p>
          </li>
        ))}
      </ul>
      <LoadMore {...more} />
    </>
  );
}
