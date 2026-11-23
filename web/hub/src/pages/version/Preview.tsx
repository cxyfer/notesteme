// 版本內容的預覽：字幕是句子列表、筆記用 NoteMarkdown（不允許原始 HTML，HREP-R18）、章節是純文字。
import { NoteMarkdown } from "@shared/notes/NoteMarkdown";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { requestText } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import type { VersionView } from "../../api/types";
import { Button, ErrorCallout, Spinner } from "../../components";
import { formatTimecode } from "../../i18n";
import { downloadPath, parseCuesJSON } from "../shared/cues";

const PAGE = 300;

function format(kind: string) {
  return kind === "transcript" ? "cues-json" : kind === "note" ? "md" : "json";
}

export function Preview({ version }: { version: VersionView }) {
  const { t } = useTranslation();
  const [limit, setLimit] = useState(PAGE);
  const f = format(version.kind);
  const q = useQuery({
    queryKey: queryKeys.versionText(version.id, f),
    queryFn: ({ signal }) =>
      requestText(downloadPath(version.id), { query: { format: f }, signal }),
    enabled: version.kind !== "frames",
    staleTime: Infinity,
  });
  if (version.kind === "frames") return <p className="secondary">{t("version.noPreview")}</p>;
  if (q.isPending) return <Spinner showLabel />;
  if (q.isError) return <ErrorCallout error={q.error} />;
  const lang = version.lang || undefined;
  if (version.kind === "note")
    return (
      <NoteMarkdown markdown={q.data} lang={lang} headingOffset={2} label={t("version.preview")} />
    );
  if (version.kind === "transcript") {
    let cues;
    try {
      cues = parseCuesJSON(q.data).cues;
    } catch {
      return <ErrorCallout error={null} message={t("version.badContent")} />;
    }
    return (
      <>
        <ol className="cue-list cue-list--static" lang={lang}>
          {cues.slice(0, limit).map((c, i) => (
            <li key={i} className="cue">
              <span className="cue__time num">{formatTimecode(c.start)}</span>
              <span className="cue__text">{c.text}</span>
            </li>
          ))}
        </ol>
        {cues.length > limit && (
          <Button onClick={() => setLimit((n) => n + PAGE)}>
            {t("version.showMore", { count: cues.length - limit })}
          </Button>
        )}
      </>
    );
  }
  return (
    <pre className="raw" lang={lang}>
      {q.data}
    </pre>
  );
}
