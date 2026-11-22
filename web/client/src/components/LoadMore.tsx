// 分頁載入器：列表底部的「已載入 N／總共 M」與「載入更多」（無限捲動之外，鍵盤與螢幕閱讀器也能載入下一頁）。
import { useTranslation } from "react-i18next";

import { formatNumber } from "../i18n";
import { Button } from "./Button";
import { ErrorCallout } from "./ErrorCallout";
import { Spinner } from "./Spinner";

export interface LoadMoreProps {
  loaded: number;
  total: number;
  hasMore: boolean;
  loading: boolean;
  error?: unknown;
  onLoadMore: () => void;
}

export function LoadMore({ loaded, total, hasMore, loading, error, onLoadMore }: LoadMoreProps) {
  const { t } = useTranslation();
  return (
    <div className="load-more">
      <p className="load-more__count secondary num">
        {t("common.loadedOf", { loaded: formatNumber(loaded), total: formatNumber(total) })}
      </p>
      {error ? (
        <ErrorCallout
          error={error}
          live={false}
          actions={[{ label: t("common.retry"), onClick: onLoadMore }]}
        />
      ) : loading ? (
        <Spinner label={t("common.loadingMore")} showLabel />
      ) : (
        hasMore && <Button onClick={onLoadMore}>{t("common.loadMore")}</Button>
      )}
    </div>
  );
}
