// 字幕樣式列表（docs/ui/pages.md §9）：內建樣式在前（不能修改，只能複製成自訂），接著是自訂樣式。
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { Button, EmptyState, ErrorCallout, Spinner } from "../../components";
import { usePageTitle } from "../../shell/usePageTitle";
import { CopyStyleDialog, ImportStyleDialog } from "./StyleDialogs";
import type { Style } from "./styleModel";

export function StylesPage() {
  const { t } = useTranslation();
  usePageTitle(t("pages.styles.title"));
  const styles = useQuery({
    queryKey: queryKeys.assStyles.list(),
    queryFn: ({ signal }) => api.get("/api/ass-styles", { signal }),
  });
  const [copyFrom, setCopyFrom] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);
  const all = styles.data?.styles ?? [];
  const builtin = all.filter((s) => s.builtin);
  const custom = all.filter((s) => !s.builtin);
  return (
    <>
      <div className="page__head">
        <h1 className="page__title">{t("pages.styles.title")}</h1>
        <Button onClick={() => setImporting(true)}>{t("styles.importOpen")}</Button>
      </div>
      <p className="secondary page__lead">{t("styles.lead")}</p>
      {styles.isPending && <Spinner label={t("common.loading")} />}
      {styles.error && (
        <ErrorCallout
          error={styles.error}
          actions={[{ label: t("common.retry"), onClick: () => void styles.refetch() }]}
        />
      )}
      {styles.data && (
        <>
          <section className="tune-section" aria-labelledby="styles-builtin">
            <h2 id="styles-builtin" className="tune-section__title">
              {t("styles.builtin")}
            </h2>
            <ul className="tune-list">
              {builtin.map((s) => (
                <StyleRow key={s.id} style={s} onCopy={() => setCopyFrom(s.id)} />
              ))}
            </ul>
          </section>
          <section className="tune-section" aria-labelledby="styles-custom">
            <h2 id="styles-custom" className="tune-section__title">
              {t("styles.custom")}
            </h2>
            {custom.length === 0 ? (
              <EmptyState
                message={t("styles.emptyCustom")}
                action={{ label: t("styles.importOpen"), onClick: () => setImporting(true) }}
              />
            ) : (
              <ul className="tune-list">
                {custom.map((s) => (
                  <StyleRow key={s.id} style={s} onCopy={() => setCopyFrom(s.id)} />
                ))}
              </ul>
            )}
          </section>
        </>
      )}
      {copyFrom && (
        <CopyStyleDialog
          key={copyFrom}
          from={copyFrom}
          open
          onOpenChange={(o) => !o && setCopyFrom(null)}
        />
      )}
      {importing && <ImportStyleDialog open onOpenChange={setImporting} />}
    </>
  );
}

function StyleRow({ style, onCopy }: { style: Style; onCopy: () => void }) {
  const { t } = useTranslation();
  const l = style.layout;
  return (
    <li className="tune-row">
      <div className="tune-row__main">
        <Link to="/styles/$id" params={{ id: style.id }} className="tune-row__title">
          {style.name}
        </Link>
        {style.builtin && <span className="tag">{t("styles.builtinTag")}</span>}
      </div>
      <p className="tune-row__meta secondary">
        {t("styles.summary", {
          show: t(`styles.show.${l.show}`),
          primary: `${style.primary.font} ${style.primary.size}`,
          translation: `${style.translation.font} ${style.translation.size}`,
        })}
      </p>
      <div className="tune-row__actions">
        <Button variant="text" onClick={onCopy}>
          {style.builtin ? t("styles.copyToCustom") : t("styles.duplicate")}
        </Button>
      </div>
    </li>
  );
}
