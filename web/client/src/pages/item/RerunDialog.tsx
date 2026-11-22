// 「重新處理」對話框（LIB-R11、PIPE-R7、PRF-R3、LLM-R8）：從哪個步驟開始、做到哪裡、處理方案或單次覆寫
// （provider／model／prompt 版本）、只重跑指定的 chunk。會產生新版本，舊版本保留。
//
//   - 費用預估：rerun 的 API 沒有 estimate 欄位，顯示「無法預估」（缺口見 docs/ui/README.md 10.7）。
//   - provider 沒有列表 API：用文字輸入，建議值取自處理方案的設定。
//   - 422 的 errors[] 逐筆顯示（chunks[i] 的訊息裡有現有的編號）；其他錯誤用 describeError。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { describeError } from "../../api/describeError";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, TextField, useToast } from "../../components";
import { STAGES } from "../library/filters";

/** 可以指定 chunk 與覆寫 provider／model／prompt 的 LLM 步驟 */
export const LLM_STAGES = ["segment", "correct", "translate"] as const;

/** 「2, 5-7」→ [2, 5, 6, 7]；格式不對回傳 null（空字串回傳 []） */
export function parseChunks(text: string): number[] | null {
  const out = new Set<number>();
  for (const raw of text.split(/[,，\s]+/)) {
    const tok = raw.trim();
    if (!tok) continue;
    const m = /^(\d+)(?:\s*[-–~]\s*(\d+))?$/.exec(tok);
    if (!m) return null;
    const a = Number(m[1]);
    const b = m[2] ? Number(m[2]) : a;
    if (a < 1 || b < a || b - a > 1000) return null;
    for (let i = a; i <= b; i++) out.add(i);
  }
  return [...out].sort((x, y) => x - y);
}

interface FieldError {
  path?: string;
  message?: string;
}

export interface RerunDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  itemId: string;
  /** 預選的起始步驟 */
  from?: string;
  /** 影音實際有的步驟（依順序） */
  stages: readonly string[];
}

export function RerunDialog({ open, onOpenChange, itemId, from, stages }: RerunDialogProps) {
  const { t } = useTranslation();
  // 每次打開都是新的表單
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("item.rerun.title")}
      description={t("item.rerun.description")}
    >
      {open && (
        <RerunForm
          itemId={itemId}
          initialFrom={from}
          stages={stages}
          onDone={() => onOpenChange(false)}
        />
      )}
    </Dialog>
  );
}

function RerunForm({
  itemId,
  initialFrom,
  stages,
  onDone,
}: {
  itemId: string;
  initialFrom?: string;
  stages: readonly string[];
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const qc = useQueryClient();
  const toast = useToast();
  const choices = useMemo(() => {
    const known = (STAGES as readonly string[]).filter(
      (s) => s !== "metadata" && (stages.length === 0 || stages.includes(s)),
    );
    return known.length ? known : STAGES.filter((s) => s !== "metadata");
  }, [stages]);
  const [from, setFrom] = useState(
    initialFrom && choices.includes(initialFrom) ? initialFrom : (choices[0] ?? "transcript"),
  );
  const [until, setUntil] = useState("");
  const [profile, setProfile] = useState("");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("");
  const [promptVersion, setPromptVersion] = useState("");
  const [chunksText, setChunksText] = useState("");
  const isLlm = (LLM_STAGES as readonly string[]).includes(from);
  const chunks = isLlm ? parseChunks(chunksText) : [];

  const profiles = useQuery({
    queryKey: queryKeys.profiles,
    queryFn: ({ signal }) => api.get("/api/profiles", { signal }),
    staleTime: 60_000,
  });
  const prompts = useQuery({
    queryKey: queryKeys.prompts,
    queryFn: ({ signal }) => api.get("/api/prompts", { signal }),
    staleTime: 60_000,
    enabled: isLlm,
  });
  const promptList = (prompts.data?.prompts ?? []).filter((p) => p.kind === from);
  const chosenPrompt = promptList.find((p) => p.id === prompt);

  // provider 的建議值：處理方案設定中出現過的 provider
  const providerHints = useMemo(() => {
    const set = new Set<string>();
    for (const p of profiles.data?.profiles ?? []) {
      const st = (p.config as { stages?: Record<string, { provider?: unknown }> }).stages;
      for (const v of Object.values(st ?? {}))
        if (typeof v?.provider === "string" && v.provider) set.add(v.provider);
    }
    return [...set].sort();
  }, [profiles.data]);

  const until_ = until && choices.indexOf(until) >= choices.indexOf(from) ? until : "";
  const rerun = useMutation({
    mutationFn: () => {
      const ov: Record<string, string> = {};
      if (isLlm) {
        if (provider.trim()) ov.provider = provider.trim();
        if (model.trim()) ov.model = model.trim();
        if (prompt) ov.prompt = promptVersion ? `${prompt}@${promptVersion}` : prompt;
      }
      return api.post<"/api/items/{id}/rerun">(apiPath("/api/items/{id}/rerun", { id: itemId }), {
        body: {
          from,
          ...(until_ ? { until: until_ } : {}),
          ...(profile ? { profile } : {}),
          ...(Object.keys(ov).length ? { overrides: { stages: { [from]: ov } } } : {}),
          ...(chunks && chunks.length ? { chunks } : {}),
        },
      });
    },
    onSuccess: (d) => {
      qc.setQueryData(queryKeys.items.detail(itemId), d);
      void qc.invalidateQueries({ queryKey: queryKeys.items.detail(itemId) });
      void qc.invalidateQueries({ queryKey: queryKeys.items.runs(itemId) });
      void qc.invalidateQueries({ queryKey: queryKeys.queue });
      toast.show({
        title: t("item.rerun.queued", { stage: t(`stage.${from}`, { defaultValue: from }) }),
      });
      onDone();
    },
  });

  const fieldErrors: FieldError[] =
    isApiError(rerun.error) && rerun.error.status === 422 ? (rerun.error.body.errors ?? []) : [];
  const chunkErrors = fieldErrors.filter((e) => e.path?.startsWith("chunks"));
  const otherErrors = fieldErrors.filter((e) => !e.path?.startsWith("chunks"));

  function submit(e: FormEvent) {
    e.preventDefault();
    if (chunks === null) return;
    rerun.mutate();
  }

  const stageName = (s: string) => t(`stage.${s}`, { defaultValue: s });
  return (
    <form className="form rerun" onSubmit={submit} noValidate>
      <div className="field">
        <label className="field__label" htmlFor={`${id}-from`}>
          {t("item.rerun.from")}
        </label>
        <select
          id={`${id}-from`}
          className="field__input"
          value={from}
          onChange={(e) => setFrom(e.target.value)}
        >
          {choices.map((s) => (
            <option key={s} value={s}>
              {stageName(s)}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label className="field__label" htmlFor={`${id}-until`}>
          {t("item.rerun.until")}
        </label>
        <select
          id={`${id}-until`}
          className="field__input"
          value={until_}
          onChange={(e) => setUntil(e.target.value)}
        >
          <option value="">{t("item.rerun.untilEnd")}</option>
          {choices.slice(choices.indexOf(from)).map((s) => (
            <option key={s} value={s}>
              {stageName(s)}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label className="field__label" htmlFor={`${id}-profile`}>
          {t("item.rerun.profile")}
        </label>
        <select
          id={`${id}-profile`}
          className="field__input"
          value={profile}
          onChange={(e) => setProfile(e.target.value)}
        >
          <option value="">{t("item.rerun.profileCurrent")}</option>
          {(profiles.data?.profiles ?? []).map((p) => (
            <option key={p.name} value={p.name}>
              {t("item.rerun.profileVersion", { name: p.name, version: p.version })}
            </option>
          ))}
        </select>
        {profiles.isError && <span className="field__hint">{t("add.profilesUnavailable")}</span>}
      </div>

      {isLlm && (
        <fieldset className="rerun__overrides">
          <legend>{t("item.rerun.overrides", { stage: stageName(from) })}</legend>
          <TextField
            label={t("item.rerun.provider")}
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
            list={`${id}-providers`}
            hint={t("item.rerun.providerHint")}
            autoComplete="off"
          />
          <datalist id={`${id}-providers`}>
            {providerHints.map((p) => (
              <option key={p} value={p} />
            ))}
          </datalist>
          <TextField
            label={t("item.rerun.model")}
            value={model}
            onChange={(e) => setModel(e.target.value)}
            autoComplete="off"
          />
          <div className="field">
            <label className="field__label" htmlFor={`${id}-prompt`}>
              {t("item.rerun.prompt")}
            </label>
            <select
              id={`${id}-prompt`}
              className="field__input"
              value={prompt}
              onChange={(e) => {
                setPrompt(e.target.value);
                setPromptVersion("");
              }}
            >
              <option value="">{t("item.rerun.promptProfile")}</option>
              {promptList.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.id}
                </option>
              ))}
            </select>
          </div>
          {chosenPrompt && (
            <div className="field">
              <label className="field__label" htmlFor={`${id}-pver`}>
                {t("item.rerun.promptVersion")}
              </label>
              <select
                id={`${id}-pver`}
                className="field__input"
                value={promptVersion}
                onChange={(e) => setPromptVersion(e.target.value)}
              >
                <option value="">
                  {t("item.rerun.promptDefault", { version: chosenPrompt.default_version })}
                </option>
                {Array.from({ length: chosenPrompt.latest_version }, (_, i) => i + 1).map((v) => (
                  <option key={v} value={String(v)}>
                    {t("item.rerun.promptV", { version: v })}
                  </option>
                ))}
              </select>
            </div>
          )}
          <TextField
            label={t("item.rerun.chunks")}
            value={chunksText}
            onChange={(e) => setChunksText(e.target.value)}
            hint={t("item.rerun.chunksHint")}
            inputMode="numeric"
            autoComplete="off"
            error={
              chunks === null
                ? t("item.rerun.chunksInvalid")
                : chunkErrors.length > 0
                  ? chunkErrors.map((e) => e.message).join(" ")
                  : undefined
            }
          />
        </fieldset>
      )}

      <p className="rerun__estimate secondary">{t("item.rerun.noEstimate")}</p>

      {rerun.isError && chunkErrors.length === 0 && (
        <ErrorCallout
          error={rerun.error}
          message={
            otherErrors.length
              ? otherErrors
                  .map((e) =>
                    e.path
                      ? t("item.rerun.fieldError", { path: e.path, message: e.message ?? "" })
                      : (e.message ?? ""),
                  )
                  .join(" ")
              : describeError(t, rerun.error)
          }
        />
      )}

      <div className="form__actions">
        <Button variant="primary" type="submit" disabled={rerun.isPending || chunks === null}>
          {rerun.isPending
            ? t("item.rerun.submitting")
            : t("item.rerun.submit", { stage: stageName(from) })}
        </Button>
      </div>
    </form>
  );
}
