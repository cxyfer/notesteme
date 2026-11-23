// 影片頁（HREP-R17）：這部影片所有看得到的版本，依種類、變體、語言分組，組內依分數排序（伺服器已排好）。
// 勾選兩個字幕版本可以並排比較（?compare=a,b；時間帶用 web/shared 的 StatusBand）。
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { queryKeys } from "../../api/queryKeys";
import { ErrorCallout, Spinner } from "../../components";
import { hasPermission } from "../../api/types";
import { useMe } from "../../shell/useMe";
import { usePageTitle } from "../../shell/usePageTitle";
import { VersionSummary } from "../shared/VersionSummary";
import { kindLabel, videoLabel } from "../versionText";
import { CompareView } from "./CompareView";
import type { VideoSearch } from "./search";

export function VideoPage() {
  const { t } = useTranslation();
  const { platform, externalId } = useParams({ from: "/videos/$platform/$externalId" });
  const search = useSearch({ from: "/videos/$platform/$externalId" });
  const navigate = useNavigate({ from: "/videos/$platform/$externalId" });
  const me = useMe();
  const q = useQuery({
    queryKey: queryKeys.video(platform, externalId, search.part),
    queryFn: ({ signal }) =>
      api.get<"/api/v1/videos/{platform}/{external_id}">(
        apiPath("/api/v1/videos/{platform}/{external_id}", { platform, external_id: externalId }),
        { query: { part: search.part }, signal },
      ),
  });
  const title =
    q.data?.video.title ||
    videoLabel({ platform, external_id: externalId, part: search.part ?? null });
  usePageTitle(title);

  // 勾選先記在 local state；湊滿兩個才寫進 URL（可以加書籤或分享）
  const fromUrl = search.compare ? search.compare.split(",") : [];
  const [picked, setPicked] = useState<string[]>(fromUrl);
  const [pickedFrom, setPickedFrom] = useState(search.compare);
  if (pickedFrom !== search.compare) {
    setPickedFrom(search.compare);
    setPicked(fromUrl);
  }
  const selected = picked;
  const toggle = (id: string) => {
    const next = selected.includes(id)
      ? selected.filter((x) => x !== id)
      : [...selected, id].slice(-2);
    setPicked(next);
    void navigate({
      search: (s: VideoSearch) => ({
        ...s,
        compare: next.length === 2 ? next.join(",") : undefined,
      }),
      replace: true,
    });
  };
  const canDownload = hasPermission(me.data, "download");

  if (q.isPending) return <Spinner showLabel />;
  if (q.isError) return <ErrorCallout error={q.error} />;
  const { video, groups } = q.data;
  const all = (groups ?? []).flatMap((g) => g.versions ?? []);
  const pair = fromUrl
    .map((id) => all.find((v) => v.id === id))
    .filter((v): v is NonNullable<typeof v> => Boolean(v));

  return (
    <section aria-labelledby="video-title">
      <h1 id="video-title" className="page__title">
        {video.title || videoLabel(video)}
      </h1>
      <p className="secondary">
        <span className="mono">{videoLabel(video)}</span>
        {video.channel_title ? ` · ${video.channel_title}` : ""}
      </p>
      {pair.length === 2 && canDownload ? (
        <CompareView
          a={pair[0]!}
          b={pair[1]!}
          onClose={() =>
            void navigate({
              search: (s: VideoSearch) => ({ ...s, compare: undefined }),
              replace: true,
            })
          }
        />
      ) : (
        <p className="secondary">
          {canDownload ? t("video.compareHint") : t("video.compareNeedsDownload")}
        </p>
      )}
      {(groups ?? []).map((g) => {
        const id = `group-${g.kind}-${g.variant}-${g.lang}`;
        return (
          <section key={id} aria-labelledby={id} className="version-group">
            <h2 id={id}>{kindLabel(t, g.kind, g.variant, g.lang)}</h2>
            <ol className="version-list">
              {(g.versions ?? []).map((v) => (
                <li key={v.id}>
                  <VersionSummary
                    v={v}
                    extra={
                      canDownload && v.kind === "transcript" ? (
                        <label className="check">
                          <input
                            type="checkbox"
                            checked={selected.includes(v.id)}
                            onChange={() => toggle(v.id)}
                          />
                          {t("video.compare")}
                        </label>
                      ) : null
                    }
                  />
                </li>
              ))}
            </ol>
          </section>
        );
      })}
    </section>
  );
}
