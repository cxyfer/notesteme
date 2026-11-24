// 內建樣式的縮圖並排比較（pages.md §9）：用同一部影音的同一張畫面，把每個內建樣式各渲染一次（POST /api/ass-styles/preview）。
//
// 每張縮圖都要跑一次 ffmpeg（後端同時只做 2 個預覽），所以控制成本：
//   - 不自動挑影音：選了影音才開始渲染（打開頁面不會花任何渲染）。
//   - 縮圖進入視窗才排進佇列（IntersectionObserver；瀏覽器沒有時視為都在視窗內）。
//   - 一次只送一個請求，前一張完成（成功或失敗）才送下一張；換影音時取消進行中的請求並重來。
// 媒體庫沒有影音時顯示空狀態，不顯示選擇器。失敗時該張顯示「預覽尚未可用」與原因，不假造圖片。
import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { api, requestBlob } from "../../api/client";
import { describeError } from "../../api/describeError";
import { queryKeys } from "../../api/queryKeys";
import { EmptyState, ItemPicker, Spinner } from "../../components";

type Thumb = { kind: "ok"; url: string } | { kind: "error"; error: unknown };

/** 和 ItemPicker 的第一頁共用快取：只用來判斷媒體庫是不是空的 */
const PICKER_LIMIT = 50;

export function BuiltinCompare({ styles }: { styles: readonly string[] }) {
  const { t } = useTranslation();
  const [item, setItem] = useState("");
  const items = useQuery({
    queryKey: queryKeys.items.picker(""),
    queryFn: ({ signal }) => api.get("/api/items", { query: { limit: PICKER_LIMIT }, signal }),
    staleTime: 60_000,
  });
  const empty = items.isSuccess && (items.data.items ?? []).length === 0;

  return (
    <section className="tune-section" aria-labelledby="styles-compare">
      <h2 id="styles-compare" className="tune-section__title">
        {t("styles.compareTitle")}
      </h2>
      <p className="secondary">{t("styles.compareLead")}</p>
      {empty ? (
        <EmptyState message={t("styles.compareNoItems")} />
      ) : (
        <>
          <div className="style-compare__controls">
            <ItemPicker value={item} onChange={setItem} />
          </div>
          {item ? (
            <CompareGrid key={item} item={item} styles={styles} />
          ) : (
            <p className="secondary">{t("styles.compareChooseItem")}</p>
          )}
        </>
      )}
    </section>
  );
}

/** 一部影音的縮圖格：換影音時整個重新掛載（key），進行中的請求取消、已產生的圖片釋放。 */
function CompareGrid({ item, styles }: { item: string; styles: readonly string[] }) {
  const [visible, setVisible] = useState<ReadonlySet<string>>(() =>
    typeof IntersectionObserver === "undefined" ? new Set(styles) : new Set(),
  );
  const [results, setResults] = useState<Readonly<Record<string, Thumb>>>({});
  // 進行中的請求（一次只有一個）；卸載時取消，並釋放所有 object URL
  const busy = useRef<AbortController | null>(null);
  const urls = useRef<string[]>([]);
  useEffect(
    () => () => {
      // 清掉 busy：StrictMode 的模擬卸載後會重新掛載，被取消的請求不會自己清
      busy.current?.abort();
      busy.current = null;
      for (const u of urls.current) URL.revokeObjectURL(u);
    },
    [],
  );

  // 序列化：沒有進行中的請求時，送出第一個在視窗內、還沒有結果的樣式；完成後 results 改變，再挑下一個
  useEffect(() => {
    if (busy.current) return;
    const next = styles.find((s) => visible.has(s) && !results[s]);
    if (!next) return;
    const ctrl = new AbortController();
    busy.current = ctrl;
    const done = (r: Thumb) => {
      busy.current = null;
      setResults((prev) => ({ ...prev, [next]: r }));
    };
    requestBlob("POST", "/api/ass-styles/preview", {
      body: { style: next, item },
      signal: ctrl.signal,
    })
      .then((blob) => {
        const url = URL.createObjectURL(blob);
        urls.current.push(url);
        done({ kind: "ok", url });
      })
      .catch((error: unknown) => {
        if (ctrl.signal.aborted) return;
        done({ kind: "error", error });
      });
  }, [visible, results, styles, item]);

  const onVisible = useCallback(
    (name: string) => setVisible((v) => (v.has(name) ? v : new Set([...v, name]))),
    [],
  );

  return (
    <ul className="style-compare">
      {styles.map((s) => (
        <ThumbCell
          key={s}
          name={s}
          result={results[s]}
          // 在視窗內、還沒有結果：正在渲染或排在前一張後面
          waiting={visible.has(s) && !results[s]}
          onVisible={onVisible}
        />
      ))}
    </ul>
  );
}

function ThumbCell({
  name,
  result,
  waiting,
  onVisible,
}: {
  name: string;
  result: Thumb | undefined;
  waiting: boolean;
  onVisible: (name: string) => void;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLLIElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        onVisible(name);
        io.disconnect();
      }
    });
    io.observe(el);
    return () => io.disconnect();
  }, [name, onVisible]);
  return (
    <li ref={ref} className="style-compare__cell">
      <figure className="style-compare__figure">
        <div className="style-compare__frame" aria-busy={waiting}>
          {result?.kind === "ok" ? (
            <img
              className="style-compare__img"
              src={result.url}
              alt={t("styles.compareAlt", { name })}
            />
          ) : result?.kind === "error" ? (
            <p className="style-compare__note">
              {t("styles.previewUnavailable", { reason: describeError(t, result.error) })}
            </p>
          ) : waiting ? (
            <Spinner label={t("styles.previewLoading")} />
          ) : (
            <p className="style-compare__note secondary">{t("styles.compareQueued")}</p>
          )}
        </div>
        <figcaption>{name}</figcaption>
      </figure>
    </li>
  );
}
