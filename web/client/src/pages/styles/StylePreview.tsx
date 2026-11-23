// 樣式預覽（pages.md §9）：後端用 ffmpeg + libass 把樣式（含還沒儲存的修改）渲染在指定影音的畫面上，回傳 PNG。
// 欄位改變後延遲 300ms 才送出；新的請求送出前取消上一個（後端同時只做 2 個預覽，舊回應晚到也不能蓋掉新的）。
// 失敗時顯示「預覽尚未可用」與原因，不假造圖片。
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { requestBlob } from "../../api/client";
import { describeError } from "../../api/describeError";
import { ErrorCallout, ItemPicker, Spinner, TextField } from "../../components";
import type { StylePatch } from "./styleModel";

export const PREVIEW_DEBOUNCE_MS = 300;

type PreviewState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ok"; url: string }
  | { kind: "error"; error: unknown };

/** 某一組輸入（key）的結果；輸入改變後、結果回來前顯示「預覽中」 */
type Settled = { key: string } & ({ kind: "ok"; url: string } | { kind: "error"; error: unknown });

export function StylePreview({
  style,
  patch,
  lang,
  disabled,
}: {
  style: string;
  patch: StylePatch | undefined;
  lang: string;
  /** 欄位有錯時不送出（後端也會回 422，但沒有必要） */
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const [item, setItem] = useState("");
  const [at, setAt] = useState("");
  const [settled, setSettled] = useState<Settled | null>(null);
  const key = JSON.stringify({ style, patch, lang, item, at });
  const active = !!item && !disabled;
  const state: PreviewState = !active
    ? { kind: "idle" }
    : settled?.key === key
      ? settled
      : { kind: "loading" };

  useEffect(() => {
    if (!active) return;
    const ctrl = new AbortController();
    let url: string | undefined;
    const h = setTimeout(() => {
      requestBlob("POST", "/api/ass-styles/preview", {
        body: {
          style,
          item,
          at: at.trim() || undefined,
          lang: lang || undefined,
          patch,
        },
        signal: ctrl.signal,
      })
        .then((blob) => {
          url = URL.createObjectURL(blob);
          setSettled({ key, kind: "ok", url });
        })
        .catch((e: unknown) => {
          if (e instanceof DOMException && e.name === "AbortError") return;
          setSettled({ key, kind: "error", error: e });
        });
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      clearTimeout(h);
      ctrl.abort();
      if (url) URL.revokeObjectURL(url);
    };
    // key 涵蓋所有輸入（patch 是物件，用序列化比較）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, active]);

  return (
    <section className="style-preview" aria-labelledby="style-preview-title">
      <h2 id="style-preview-title" className="tune-section__title">
        {t("styles.preview")}
      </h2>
      <div className="style-preview__frame" aria-live="polite" aria-busy={state.kind === "loading"}>
        {state.kind === "idle" && (
          <p className="style-preview__empty secondary">
            {disabled ? t("styles.previewFixFields") : t("styles.previewChooseItem")}
          </p>
        )}
        {state.kind === "loading" && <Spinner label={t("styles.previewLoading")} showLabel />}
        {state.kind === "ok" && (
          <img
            className="style-preview__img"
            src={state.url}
            alt={t("styles.previewAlt", { name: style, at: at.trim() || "00:00" })}
          />
        )}
        {state.kind === "error" && (
          <ErrorCallout
            error={state.error}
            live={false}
            message={t("styles.previewUnavailable", { reason: describeError(t, state.error) })}
          />
        )}
      </div>
      <div className="style-preview__controls">
        <ItemPicker value={item} onChange={setItem} />
        <TextField
          label={t("styles.previewAt")}
          value={at}
          onChange={(e) => setAt(e.target.value)}
          placeholder="06:25"
          hint={t("styles.previewAtHint")}
          inputMode="numeric"
        />
      </div>
    </section>
  );
}
