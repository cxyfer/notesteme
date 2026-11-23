// 影音詳情的「分享到 Hub」（HSHR-R2～R6）：選產出、授權、可見度、聲明勾選，並預覽會送出的欄位。
// 送出前 Client 會再檢查一次（422），沒有設定 Hub 時 409 hub_not_configured 帶「設定 Hub」的連結。
// 由 ItemPage lazy 載入。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import type { components } from "../../api/schema";
import { Button, Dialog, ErrorCallout, SelectField } from "../../components";
import { formatDate, formatTimecode } from "../../i18n";
import { SaveError } from "../settings/shared";
import "../../styles/hub.css";
import { hubLink } from "./hub";

type Item = components["schemas"]["ItemDetail"];
type Artifact = components["schemas"]["HubShareArtifact"];

export const LICENSES = ["CC-BY-4.0", "CC-BY-SA-4.0", "CC-BY-NC-4.0", "CC-BY-NC-SA-4.0", "CC0-1.0"];
const VISIBILITIES = ["", "public", "authenticated", "private"];
const SETTINGS_ROUTE = "/settings/$section";
const HUB_SECTION = "hub";

/** 這部影音可以分享的產出（依步驟狀態推測；不存在的產出由伺服器回 422 artifact_not_found） */
export function shareableArtifacts(
  d: Item,
  targets: readonly string[],
  hasEdited: boolean,
): Artifact[] {
  const done = new Set((d.stages ?? []).filter((s) => s.status === "done").map((s) => s.stage));
  const lang = d.original_lang || undefined;
  const out: Artifact[] = [];
  if (done.has("transcript")) out.push({ kind: "transcript", variant: "source", lang });
  if (done.has("normalize")) out.push({ kind: "transcript", variant: "normalized", lang });
  if (done.has("segment")) out.push({ kind: "transcript", variant: "segmented", lang });
  if (done.has("correct")) out.push({ kind: "transcript", variant: "corrected", lang });
  if (done.has("translate"))
    for (const l of targets) out.push({ kind: "transcript", variant: "translated", lang: l });
  if (hasEdited) out.push({ kind: "transcript", variant: "edited", lang });
  if (done.has("chapters")) out.push({ kind: "chapters" });
  if (done.has("note")) out.push({ kind: "note" });
  return out;
}

const key = (a: Artifact) => `${a.kind}:${a.variant ?? ""}:${a.lang ?? ""}`;

export function HubShareDialog({
  item,
  targets,
  hasEdited,
  open,
  onOpenChange,
}: {
  item: Item;
  targets: readonly string[];
  hasEdited: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const candidates = shareableArtifacts(item, targets, hasEdited);
  const [all, setAll] = useState(true);
  const [picked, setPicked] = useState<Set<string>>(() => new Set(candidates.map(key)));
  const [license, setLicense] = useState("");
  const [visibility, setVisibility] = useState("");
  const [frames, setFrames] = useState(false);
  const [attest, setAttest] = useState(false);
  const settings = useQuery({
    queryKey: queryKeys.hubSettings,
    queryFn: () => api.get("/api/settings/hub"),
    enabled: open,
  });
  const share = useMutation({
    mutationFn: () =>
      api.post<"/api/items/{id}/hub/share">(apiPath("/api/items/{id}/hub/share", { id: item.id }), {
        body: {
          license,
          attest,
          include_frames: frames || undefined,
          visibility: visibility || undefined,
          artifacts: all ? undefined : candidates.filter((a) => picked.has(key(a))),
        },
      }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: queryKeys.items.details() }),
  });
  const goToSettings = () =>
    void navigate({ to: SETTINGS_ROUTE, params: { section: HUB_SECTION } });
  const notConfigured = isApiError(share.error) && share.error.code === "hub_not_configured";
  const hubUrl = settings.data?.url;
  const fields: [string, string][] = [
    [t("hub.share.f.platform"), item.platform],
    [t("hub.share.f.id"), item.external_id + ((item.part ?? 0) > 1 ? ` p${item.part}` : "")],
    [t("hub.share.f.title"), item.title],
    [t("item.meta.published"), item.published_at ? formatDate(item.published_at) : "—"],
    [t("item.meta.duration"), item.duration_s != null ? formatTimecode(item.duration_s) : "—"],
    [t("item.meta.lang"), item.original_lang || "—"],
  ];

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) share.reset();
      }}
      title={t("hub.share.title")}
      description={t("hub.share.intro")}
      footer={
        share.isSuccess ? undefined : (
          <Button
            variant="primary"
            disabled={!attest || !license || share.isPending || (!all && picked.size === 0)}
            onClick={() => share.mutate()}
          >
            {t("hub.share.submit")}
          </Button>
        )
      }
    >
      {share.isSuccess ? (
        <div role="status" className="hub-share">
          <p>{t("hub.share.done")}</p>
          <ul>
            {(share.data.versions ?? []).map((v) => {
              const href = hubLink(hubUrl, `/versions/${encodeURIComponent(v.version_id)}`);
              const label = [v.kind, v.variant, v.lang].filter(Boolean).join(" · ");
              return (
                <li key={v.version_id}>
                  {href ? (
                    <a href={href} target="_blank" rel="noopener noreferrer">
                      {label}
                    </a>
                  ) : (
                    label
                  )}
                  {v.deduplicated && <span className="hub-muted"> {t("hub.share.dedup")}</span>}
                </li>
              );
            })}
          </ul>
        </div>
      ) : (
        <form className="hub-share" onSubmit={(e) => e.preventDefault()}>
          <fieldset>
            <legend>{t("hub.share.what")}</legend>
            <label className="check">
              <input type="radio" name="hub-all" checked={all} onChange={() => setAll(true)} />
              {t("hub.share.all")}
            </label>
            <label className="check">
              <input type="radio" name="hub-all" checked={!all} onChange={() => setAll(false)} />
              {t("hub.share.some")}
            </label>
            {!all && (
              <div className="hub-share__list">
                {candidates.map((a) => (
                  <label key={key(a)} className="check">
                    <input
                      type="checkbox"
                      checked={picked.has(key(a))}
                      onChange={(e) =>
                        setPicked((s) => {
                          const n = new Set(s);
                          if (e.target.checked) n.add(key(a));
                          else n.delete(key(a));
                          return n;
                        })
                      }
                    />
                    {[a.kind, a.variant, a.lang].filter(Boolean).join(" · ")}
                  </label>
                ))}
              </div>
            )}
            <label className="check">
              <input
                type="checkbox"
                checked={frames}
                onChange={(e) => setFrames(e.target.checked)}
              />
              {t("hub.share.frames")}
            </label>
          </fieldset>
          <SelectField
            label={t("hub.share.license")}
            value={license}
            options={[
              { value: "", label: t("hub.share.pick") },
              ...LICENSES.map((l) => ({ value: l, label: l })),
            ]}
            onChange={(e) => setLicense(e.target.value)}
          />
          <SelectField
            label={t("hub.share.visibility")}
            value={visibility}
            options={VISIBILITIES.map((v) => ({
              value: v,
              label: v ? t(`hub.share.vis.${v}`) : t("hub.share.vis.default"),
            }))}
            onChange={(e) => setVisibility(e.target.value)}
          />
          <details className="hub-share__preview">
            <summary>{t("hub.share.preview")}</summary>
            <dl className="hub-facts">
              {fields.map(([k, v]) => (
                <div key={k}>
                  <dt>{k}</dt>
                  <dd>{v}</dd>
                </div>
              ))}
            </dl>
            <p className="hub-muted">{t("hub.share.previewNote")}</p>
          </details>
          <label className="check">
            <input type="checkbox" checked={attest} onChange={(e) => setAttest(e.target.checked)} />
            {t("hub.share.attest")}
          </label>
          {share.isError &&
            (notConfigured ? (
              <ErrorCallout
                error={share.error}
                actions={[
                  {
                    label: t("hub.share.configure"),
                    primary: true,
                    onClick: goToSettings,
                  },
                ]}
              />
            ) : (
              <SaveError error={share.error} />
            ))}
        </form>
      )}
    </Dialog>
  );
}
