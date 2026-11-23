// 影音詳情的「畫面」與「筆記」分頁（docs/ui/pages.md §4，M3：FRM-R10、NOTE-R10、R13、VAULT-R5）。
// 只有切到該分頁才讀取；步驟沒有執行過（404 no_frames／no_note）時顯示說明，不當成錯誤。
// 畫面與筆記都是不可信的內容：圖說以純文字顯示，筆記經 notes/markdown.ts 解析後才渲染。
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, ErrorCallout, Spinner } from "../../components";
import { formatTimecode } from "../../i18n";
import { NoteChecks } from "../../notes/NoteChecks";
import { NoteMarkdown, timeLinkSeconds } from "../../notes/NoteMarkdown";

type Frame = components["schemas"]["Frame"];

/** API 的時間 HH:MM:SS.mmm → 秒 */
export function clockSeconds(at: string): number {
  const m = /^(\d+):(\d{2}):(\d{2})(?:\.(\d+))?$/.exec(at);
  if (!m) return 0;
  return Number(m[1]) * 3600 + Number(m[2]) * 60 + Number(m[3]) + Number(`0.${m[4] ?? "0"}`);
}

function frameUrl(itemId: string, n: number) {
  return `${apiPath("/api/items/{id}/frames/{n}", { id: itemId, n })}?size=thumb`;
}

/** 404 的這些 code 代表「還沒有」，不是錯誤 */
async function orNull<T>(p: Promise<T>, code: string): Promise<T | null> {
  try {
    return await p;
  } catch (e) {
    if (isApiError(e) && e.status === 404 && (e.code === code || e.code === "not_found"))
      return null;
    throw e;
  }
}

export function useFrames(id: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.items.frames(id),
    queryFn: ({ signal }) =>
      orNull(
        api.get<"/api/items/{id}/frames">(apiPath("/api/items/{id}/frames", { id }), { signal }),
        "no_frames",
      ),
    enabled,
    staleTime: 60_000,
  });
}

export function FramesPanel({
  itemId,
  onSeek,
}: {
  itemId: string;
  onSeek: (seconds: number) => void;
}) {
  const { t } = useTranslation();
  const frames = useFrames(itemId, true);
  const [all, setAll] = useState(false);
  if (frames.isPending) return <Spinner label={t("common.loading")} showLabel />;
  if (frames.error)
    return (
      <ErrorCallout
        error={frames.error}
        actions={[{ label: t("common.retry"), onClick: () => void frames.refetch() }]}
      />
    );
  const f = frames.data;
  if (!f) return <p className="secondary">{t("item.frames.none")}</p>;
  const list = (f.frames ?? []).filter((x) => all || x.selected);
  return (
    <div className="note-layout">
      <p className="secondary">
        {t("item.frames.summary", {
          selected: f.selected_count,
          target: f.target,
          min: f.range.min,
          max: f.range.max,
          candidates: f.candidate_count,
        })}
      </p>
      {f.shortfall && <p className="secondary">{t("item.frames.shortfall")}</p>}
      <div
        className="page__actions page__actions--start"
        role="group"
        aria-label={t("item.frames.show")}
      >
        <Button
          variant={all ? "outline" : "primary"}
          aria-pressed={!all}
          onClick={() => setAll(false)}
        >
          {t("item.frames.selectedOnly")}
        </Button>
        <Button
          variant={all ? "primary" : "outline"}
          aria-pressed={all}
          onClick={() => setAll(true)}
        >
          {t("item.frames.allCandidates")}
        </Button>
      </div>
      {list.length === 0 ? (
        <p className="secondary">{t("item.frames.noneSelected")}</p>
      ) : (
        <ul className="contact-sheet" aria-label={t("item.frames.sheet")}>
          {list.map((x) => (
            <FrameCard key={x.n} itemId={itemId} frame={x} onSeek={onSeek} />
          ))}
        </ul>
      )}
    </div>
  );
}

function FrameCard({
  itemId,
  frame,
  onSeek,
}: {
  itemId: string;
  frame: Frame;
  onSeek: (s: number) => void;
}) {
  const { t } = useTranslation();
  const s = clockSeconds(frame.at);
  const time = formatTimecode(s);
  return (
    <li>
      <figure className="contact-sheet__frame" data-selected={frame.selected}>
        <img
          src={frameUrl(itemId, frame.n)}
          alt={frame.caption || t("item.frames.alt", { n: frame.n, time })}
          loading="lazy"
          width={frame.width}
          height={frame.height}
        />
        <figcaption>
          <Button variant="text" className="contact-sheet__time" onClick={() => onSeek(s)}>
            {t("item.frames.jump", { time })}
          </Button>
          {frame.selected ? (
            <>
              {" "}
              <span className="tag tag--strong">{t("item.frames.selected")}</span>
              {frame.chapter && <span className="secondary"> {frame.chapter}</span>}
            </>
          ) : (
            frame.excluded && (
              <span className="secondary">
                {" "}
                {t(`item.frames.excluded.${frame.excluded}`, { defaultValue: frame.excluded })}
              </span>
            )
          )}
          {frame.caption && <span className="contact-sheet__caption"> {frame.caption}</span>}
        </figcaption>
      </figure>
    </li>
  );
}

export function NotePanel({
  itemId,
  itemUrl,
  onSeek,
  onRegenerate,
}: {
  itemId: string;
  itemUrl?: string;
  onSeek: (seconds: number) => void;
  onRegenerate: () => void;
}) {
  const { t } = useTranslation();
  const note = useQuery({
    queryKey: queryKeys.items.note(itemId),
    queryFn: ({ signal }) =>
      orNull(
        api.get<"/api/items/{id}/note">(apiPath("/api/items/{id}/note", { id: itemId }), {
          signal,
        }),
        "no_note",
      ),
    staleTime: 60_000,
  });
  if (note.isPending) return <Spinner label={t("common.loading")} showLabel />;
  if (note.error)
    return (
      <ErrorCallout
        error={note.error}
        actions={[{ label: t("common.retry"), onClick: () => void note.refetch() }]}
      />
    );
  const n = note.data;
  if (!n) return <p className="secondary">{t("item.note.none")}</p>;
  const outputs = n.outputs ?? [];
  return (
    <div className="note-layout">
      {n.stale && (
        <ErrorCallout
          error={null}
          live={false}
          category="needs_action"
          message={n.stale_reason || t("item.note.stale")}
          actions={[{ label: t("item.note.regenerate"), onClick: onRegenerate }]}
        />
      )}
      <section aria-labelledby="note-checks-title">
        <h2 id="note-checks-title" className="tune-section__title">
          {t("item.note.checks")}
        </h2>
        <NoteChecks checks={n.checks ?? []} />
      </section>
      <section aria-labelledby="note-outputs-title">
        <h2 id="note-outputs-title" className="tune-section__title">
          {t("item.note.written")}
        </h2>
        {outputs.length === 0 ? (
          <p className="secondary">{t("item.note.notWritten")}</p>
        ) : (
          <ul className="var-list">
            {outputs.map((o) => (
              <li key={`${o.vault}:${o.path}`}>
                {t("item.note.output", {
                  vault: o.vault,
                  state: t(`item.note.state.${o.state}`, { defaultValue: o.state }),
                })}{" "}
                <code className="break-anywhere">{o.path}</code>
                {o.commit && (
                  <span className="secondary">
                    {" "}
                    {t("item.note.commit", { commit: o.commit.slice(0, 12) })}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
        <a
          className="button button--outline"
          href={`${apiPath("/api/items/{id}/note", { id: itemId })}?format=md`}
          download
        >
          {t("item.note.download")}
        </a>
      </section>
      <NoteMarkdown
        markdown={n.markdown}
        lang={n.lang || undefined}
        label={t("item.note.content")}
        onSeek={onSeek}
        onTimeLink={(href) => timeLinkSeconds(itemUrl, href)}
        resolveFrame={(num) => ({
          src: frameUrl(itemId, num),
          alt: t("item.frames.altShort", { n: num }),
        })}
      />
    </div>
  );
}
