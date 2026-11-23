// 兩個字幕版本並排比較（HREP 的 Hub UI：「兩個版本並排比較，時間帶與字幕句列表用 web/shared 的元件」）。
// 時間帶（StatusBand 完整版）的兩條帶子是兩個版本有字幕的時間範圍；拖動播放頭或點一句字幕，兩邊都跳到同一個時間，
// 下方用 DiffText 顯示這個時間點兩句的逐字差異。內容是不可信的文字，只以文字節點顯示。
import { diffChars, DiffText, StatusBand, type StatusBandStage } from "@shared";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { queryKeys } from "../../api/queryKeys";
import type { VersionItem } from "../../api/types";
import { Button, ErrorCallout, Spinner } from "../../components";
import { formatTimecode } from "../../i18n";
import { cueAt, fetchCues, type Cue } from "../shared/cues";
import { kindLabel } from "../versionText";

function useCues(id: string) {
  return useQuery({
    queryKey: queryKeys.versionText(id, "cues-json"),
    queryFn: ({ signal }) => fetchCues(id, signal),
    staleTime: Infinity,
  });
}

function CueColumn({
  title,
  lang,
  cues,
  current,
  onPick,
}: {
  title: string;
  lang: string;
  cues: readonly Cue[];
  current: number;
  onPick: (s: number) => void;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLOListElement>(null);
  useEffect(() => {
    const el = ref.current?.querySelector<HTMLElement>("[aria-current='true']");
    el?.scrollIntoView({ block: "nearest" });
  }, [current]);
  return (
    <section className="compare__col" aria-label={title}>
      <h3>{title}</h3>
      <ol className="cue-list" ref={ref} lang={lang || undefined}>
        {cues.map((c, i) => (
          <li key={i} aria-current={i === current ? "true" : undefined}>
            <button type="button" className="cue" onClick={() => onPick(c.start)}>
              <span
                className="cue__time num"
                aria-label={t("compare.jumpTo", { time: formatTimecode(c.start) })}
              >
                {formatTimecode(c.start)}
              </span>
              <span className="cue__text">{c.text}</span>
            </button>
          </li>
        ))}
      </ol>
    </section>
  );
}

export function CompareView({
  a,
  b,
  onClose,
}: {
  a: VersionItem;
  b: VersionItem;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const qa = useCues(a.id);
  const qb = useCues(b.id);
  const [pos, setPos] = useState(0);
  const labelA = t("compare.left", { name: kindLabel(t, a.kind, a.variant, a.lang) });
  const labelB = t("compare.right", { name: kindLabel(t, b.kind, b.variant, b.lang) });

  const stages = useMemo<StatusBandStage[]>(() => {
    const seg = (cues: readonly Cue[]) =>
      cues.map((c) => ({ from: c.start, to: c.end, status: "done" as const }));
    return [
      { key: "a", status: "done", label: labelA, segments: seg(qa.data?.cues ?? []) },
      { key: "b", status: "done", label: labelB, segments: seg(qb.data?.cues ?? []) },
    ];
  }, [qa.data, qb.data, labelA, labelB]);

  if (qa.isPending || qb.isPending) return <Spinner showLabel label={t("compare.loading")} />;
  if (qa.isError || qb.isError) return <ErrorCallout error={qa.error ?? qb.error} />;
  const ca = qa.data.cues;
  const cb = qb.data.cues;
  const duration = Math.max(ca.at(-1)?.end ?? 0, cb.at(-1)?.end ?? 0);
  const ia = cueAt(ca, pos);
  const ib = cueAt(cb, pos);
  const ta = ca[ia]?.text ?? "";
  const tb = cb[ib]?.text ?? "";

  return (
    <section className="compare" aria-labelledby="compare-title">
      <div className="compare__head">
        <h2 id="compare-title">{t("compare.title")}</h2>
        <Button variant="text" onClick={onClose}>
          {t("compare.close")}
        </Button>
      </div>
      <StatusBand
        variant="full"
        stages={stages}
        label={t("compare.band")}
        durationS={duration || null}
        position={pos}
        onSeek={setPos}
        formatTime={formatTimecode}
        valueText={(f) => t("compare.position", { time: f })}
      />
      <div className="compare__diff" aria-live="polite">
        <p className="secondary">{t("compare.diffAt", { time: formatTimecode(pos) })}</p>
        <p>
          <DiffText
            parts={diffChars(ta, tb)}
            lang={qb.data.lang || undefined}
            labels={{ del: t("compare.onlyLeft"), ins: t("compare.onlyRight") }}
          />
        </p>
      </div>
      <div className="compare__cols">
        <CueColumn title={labelA} lang={qa.data.lang} cues={ca} current={ia} onPick={setPos} />
        <CueColumn title={labelB} lang={qb.data.lang} cues={cb} current={ib} onPick={setPos} />
      </div>
    </section>
  );
}
