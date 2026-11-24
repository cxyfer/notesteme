// 處理方案編輯頁（docs/ui/pages.md §8）：流程列、編號的步驟區塊、複製、儲存變更、設為預設。
//
// - 表單編輯 config（這一層）的副本；沒寫的欄位沿用全域設定。儲存時送 RFC 7396 merge patch（只有改過的鍵）。
// - 樂觀鎖：PATCH 沒有 If-Match，所以儲存前先讀一次目前的版本，和開始編輯時不同就停下來讓使用者決定；
//   儲存後回應的版本不是「開始時 + 1」時，也明確告知（兩次請求之間仍有很小的窗口，見 docs/ui/README.md 10.x）。
// - 422 的 errors[].path（例如 stages.correct.provider）顯示在對應欄位旁。
// - 步驟：字幕來源、分段、校正、翻譯、關鍵畫面、筆記、輸出（pages.md §8 的線框）。關鍵畫面與筆記的專屬欄位
//   只在步驟寫進處理方案（選了執行方式）後顯示：筆記的預設 executor 是 api，只寫 template 會被 schema 要求
//   provider 與 model（PRF-R5），沿用全域設定時不能單獨寫子欄位。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { useHooks, useVaults } from "../../api/outputCatalog";
import { fieldErrorsOf, type FieldErrors } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import {
  Button,
  Dialog,
  ErrorCallout,
  SelectField,
  Spinner,
  TextField,
  useToast,
} from "../../components";
import { usePageTitle } from "../../shell/usePageTitle";
import { NotFoundContent } from "../NotFoundPage";
import { NewProfileDialog } from "./ProfileDialogs";
import {
  CAPTION_KINDS,
  clone,
  getPath,
  isObject,
  knownProviders,
  LLM_STAGES,
  NOTE_ENGINES,
  mergePatch,
  ON_MANUAL,
  ON_USER_CHANGE,
  OUTPUT_FORMATS,
  OUTPUT_TARGETS,
  retarget,
  parsePromptRef,
  setPath,
  splitList,
  SPLIT_TIMING,
  stageExecutor,
  stageExecutors,
  VARIANT_STRATEGY,
  type Config,
  type LlmStage,
  type Profile,
} from "./profileModel";

const route = getRouteApi("/app/profiles/$id");

// 表單用到的設定路徑（PRF-R1；422 的 errors[].path 也是這個格式）
const P = {
  splitTiming: "timing.split_timing",
  onManual: "stages.segment.on_manual",
  minSimilarity: "stages.correct.min_similarity",
  targets: "language.targets",
  variantStrategy: "language.variant_strategy",
  captionPriority: "language.caption_priority",
  allowPlatformTranslated: "language.allow_platform_translated",
  fallback: "language.fallback",
  framesPerMinute: "stages.frames.per_minute",
  framesMax: "stages.frames.max_frames",
  noteTemplate: "stages.note.template",
  noteEngine: "stages.note.engine",
  noteLang: "stages.note.lang",
  noteIncludeTranslation: "stages.note.include_translation",
} as const;
const CONFIG_ROOT = "config";
const NEW_CAPTION_FIRST = { lang: "original", kind: "manual" };
const NEW_CAPTION_NEXT = { lang: "original", kind: "auto" };
const NEW_OUTPUT = { target: "sidecar", formats: ["srt"] };
const TRI = { inherit: "", yes: "yes", no: "no" } as const;
const FALLBACK_MODE = { inherit: "", none: "none", custom: "custom" } as const;
type FallbackMode = (typeof FALLBACK_MODE)[keyof typeof FALLBACK_MODE];

interface EditState {
  base: Config;
  version: number;
  draft: Config;
}

export function ProfileEditorPage() {
  const { t } = useTranslation();
  const { id } = route.useParams();
  usePageTitle(`${id} · ${t("pages.profiles.title")}`);
  const profile = useQuery({
    queryKey: queryKeys.profile(id),
    queryFn: ({ signal }) =>
      api.get<"/api/profiles/{id}">(apiPath("/api/profiles/{id}", { id }), { signal }),
  });
  const heading = <h1 className="page__title">{id}</h1>;
  if (profile.isPending) {
    return (
      <>
        {heading}
        <Spinner label={t("common.loading")} />
      </>
    );
  }
  if (profile.error) {
    if (isApiError(profile.error) && profile.error.status === 404) return <NotFoundContent />;
    return (
      <>
        {heading}
        <ErrorCallout
          error={profile.error}
          actions={[{ label: t("common.retry"), onClick: () => void profile.refetch() }]}
        />
      </>
    );
  }
  return <ProfileEditor key={id} heading={heading} profile={profile.data} />;
}

function ProfileEditor({ heading, profile }: { heading: ReactNode; profile: Profile }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const id = profile.name;
  const [edit, setEdit] = useState<EditState>(() => ({
    base: clone(profile.config),
    version: profile.version,
    draft: clone(profile.config),
  }));
  const [conflict, setConflict] = useState<Profile | null>(null);
  const [copying, setCopying] = useState(false);
  const draft = edit.draft;
  const patch = mergePatch(edit.base, draft);

  const prompts = useQuery({
    queryKey: queryKeys.prompts.list(),
    queryFn: ({ signal }) => api.get("/api/prompts", { signal }),
  });
  const styles = useQuery({
    queryKey: queryKeys.assStyles.list(),
    queryFn: ({ signal }) => api.get("/api/ass-styles", { signal }),
  });
  // 筆記模板（NOTE-R11）：和筆記模板頁共用 query；讀不到時模板欄位只列已寫的值
  const templates = useQuery({
    queryKey: queryKeys.noteTemplates,
    queryFn: ({ signal }) => api.get("/api/note-templates", { signal }),
    retry: false,
  });

  // 放棄修改或重新載入時遞增，讓步驟區塊重新掛載（文字欄位的暫存內容一起重設）
  const [formKey, setFormKey] = useState(0);
  function reset(p: Profile) {
    setFormKey((k) => k + 1);
    setEdit({ base: clone(p.config), version: p.version, draft: clone(p.config) });
    qc.setQueryData(queryKeys.profile(p.name), p);
  }

  const save = useMutation({
    mutationFn: async ({ force }: { force: boolean }) => {
      if (!patch) return null;
      if (!force) {
        const latest = await api.get<"/api/profiles/{id}">(apiPath("/api/profiles/{id}", { id }));
        if (latest.version !== edit.version) return { kind: "conflict" as const, latest };
      }
      const saved = await api.patch<"/api/profiles/{id}">(apiPath("/api/profiles/{id}", { id }), {
        body: { config: patch },
      });
      return { kind: "saved" as const, saved, expected: edit.version + 1 };
    },
    onSuccess: async (res) => {
      if (!res) return;
      if (res.kind === "conflict") {
        setConflict(res.latest);
        return;
      }
      setConflict(null);
      reset(res.saved);
      await qc.invalidateQueries({ queryKey: queryKeys.profiles });
      toast.show({
        title:
          res.saved.version === res.expected
            ? t("profiles.saved", { version: res.saved.version })
            : t("profiles.savedConcurrent", { version: res.saved.version }),
      });
    },
  });
  const setDefault = useMutation({
    mutationFn: () =>
      api.post<"/api/profiles/{id}/default">(apiPath("/api/profiles/{id}/default", { id })),
    onSuccess: async (p) => {
      reset(p);
      await qc.invalidateQueries({ queryKey: queryKeys.profiles });
      toast.show({ title: t("common.setAsDefaultDone") });
    },
  });

  const errors = fieldErrorsOf(save.error);
  const update = (path: string, value: unknown) =>
    setEdit((e) => ({ ...e, draft: setPath(e.draft, path, value) }));
  const ctx: StepCtx = {
    draft,
    update,
    errors,
    prompts: prompts.data?.prompts ?? [],
    styles: (styles.data?.styles ?? []).map((s) => s.name),
    templates: (templates.data?.templates ?? []).map((x) => ({ id: x.id, name: x.name })),
  };
  const splitTimingOptions = inheritOptions(t, SPLIT_TIMING, "profiles.splitTiming");
  const onManualOptions = inheritOptions(t, ON_MANUAL, "profiles.onManual");
  const variantOptions = inheritOptions(t, VARIANT_STRATEGY, "profiles.variantStrategy");
  const segmentWritten = !!stageExecutor(draft, "segment");
  const correctWritten = !!stageExecutor(draft, "correct");
  const framesWritten = !!stageExecutor(draft, "frames");
  const noteWritten = !!stageExecutor(draft, "note");
  const unmapped = Object.entries(errors).filter(
    ([p]) => !KNOWN_ERROR_PREFIXES.some((x) => p.startsWith(x)),
  );

  return (
    <>
      <div className="page__head">
        {heading}
        {profile.default && <span className="tag tag--strong">{t("profiles.defaultTag")}</span>}
        {profile.builtin && <span className="tag">{t("profiles.builtinTag")}</span>}
        <span className="secondary num">{t("profiles.version", { version: edit.version })}</span>
        <div className="page__actions">
          <Button onClick={() => setCopying(true)}>{t("profiles.copy")}</Button>
          {!profile.default && (
            <Button disabled={setDefault.isPending || !!patch} onClick={() => setDefault.mutate()}>
              {t("common.setAsDefault")}
            </Button>
          )}
          <Button
            variant="text"
            disabled={!patch || save.isPending}
            onClick={() => {
              setEdit((e) => ({ ...e, draft: clone(e.base) }));
              setFormKey((k) => k + 1);
              save.reset();
            }}
          >
            {t("common.discard")}
          </Button>
          <Button
            variant="primary"
            disabled={!patch || save.isPending}
            onClick={() => save.mutate({ force: false })}
          >
            {save.isPending ? t("common.saving") : t("common.saveChanges")}
          </Button>
        </div>
      </div>
      <p className="secondary page__lead">{t("profiles.editorLead")}</p>
      {patch && !profile.default && <p className="secondary">{t("profiles.saveBeforeDefault")}</p>}
      {setDefault.error && <ErrorCallout error={setDefault.error} />}
      {save.error && Object.keys(errors).length === 0 && <ErrorCallout error={save.error} />}
      {Object.keys(errors).length > 0 && (
        <ErrorCallout
          error={save.error}
          message={t("profiles.invalid", { count: Object.keys(errors).length })}
        />
      )}
      {unmapped.length > 0 && (
        <ul className="field-errors">
          {unmapped.map(([p, m]) => (
            <li key={p}>
              <code>{p || CONFIG_ROOT}</code> {m}
            </li>
          ))}
        </ul>
      )}

      <FlowBar draft={draft} />
      <ol className="steps" key={formKey}>
        <Step n={1} title={t("profiles.step.source")}>
          <SourceStep {...ctx} />
        </Step>
        <Step n={2} title={t("stage.segment")} off={stageExecutor(draft, "segment") === "off"}>
          <LlmStageFields stage="segment" {...ctx} />
          <SelectField
            label={t("profiles.field.splitTiming")}
            value={str(getPath(draft, P.splitTiming))}
            onChange={(e) => update(P.splitTiming, e.target.value || undefined)}
            options={splitTimingOptions}
            error={errors[P.splitTiming]}
          />
          {segmentWritten && (
            <SelectField
              label={t("profiles.field.onManual")}
              value={str(getPath(draft, P.onManual))}
              onChange={(e) => update(P.onManual, e.target.value || undefined)}
              options={onManualOptions}
              error={errors[P.onManual]}
            />
          )}
        </Step>
        <Step n={3} title={t("stage.correct")} off={stageExecutor(draft, "correct") === "off"}>
          <LlmStageFields stage="correct" {...ctx} />
          {correctWritten && (
            <NumberField
              label={t("profiles.field.minSimilarity")}
              hint={t("profiles.field.minSimilarityHint")}
              path={P.minSimilarity}
              min={0}
              max={1}
              step={0.01}
              {...ctx}
            />
          )}
        </Step>
        <Step n={4} title={t("stage.translate")} off={stageExecutor(draft, "translate") === "off"}>
          <LlmStageFields stage="translate" {...ctx} />
          <ListField
            label={t("profiles.field.targets")}
            hint={t("profiles.field.targetsHint")}
            path={P.targets}
            {...ctx}
          />
          <SelectField
            label={t("profiles.field.variantStrategy")}
            value={str(getPath(draft, P.variantStrategy))}
            onChange={(e) => update(P.variantStrategy, e.target.value || undefined)}
            options={variantOptions}
            error={errors[P.variantStrategy]}
          />
        </Step>
        <Step n={5} title={t("stage.frames")} off={stageExecutor(draft, "frames") === "off"}>
          <LlmStageFields stage="frames" {...ctx} />
          {framesWritten && (
            <div className="form-grid">
              <NumberField
                label={t("profiles.field.framesPerMinute")}
                hint={t("profiles.field.framesPerMinuteHint")}
                path={P.framesPerMinute}
                min={0.1}
                max={60}
                step={0.1}
                {...ctx}
              />
              <NumberField
                label={t("profiles.field.framesMax")}
                hint={t("profiles.field.framesMaxHint")}
                path={P.framesMax}
                min={1}
                max={100}
                step={1}
                {...ctx}
              />
            </div>
          )}
          <p className="secondary step__note">{t("profiles.framesRange")}</p>
        </Step>
        <Step n={6} title={t("stage.note")} off={stageExecutor(draft, "note") === "off"}>
          <LlmStageFields stage="note" {...ctx} />
          {noteWritten && <NoteFields {...ctx} />}
        </Step>
        <Step n={7} title={t("stage.publish")} off={outputsOf(draft).length === 0}>
          <OutputsStep {...ctx} />
        </Step>
      </ol>

      {conflict && (
        <Dialog
          open
          onOpenChange={(o) => !o && setConflict(null)}
          title={t("profiles.conflictTitle")}
          description={t("profiles.conflictBody", {
            name: id,
            mine: edit.version,
            theirs: conflict.version,
          })}
          footer={
            <>
              <Button
                onClick={() => {
                  reset(conflict);
                  setConflict(null);
                  save.reset();
                }}
              >
                {t("profiles.conflictReload")}
              </Button>
              <Button variant="primary" onClick={() => save.mutate({ force: true })}>
                {t("profiles.conflictOverwrite")}
              </Button>
            </>
          }
        />
      )}
      {copying && (
        <NewProfileDialog from={id} config={edit.base} onClose={() => setCopying(false)} />
      )}
    </>
  );
}

/** 422 的欄位路徑由各步驟顯示；其他的（例如 llm.*、未知欄位）列在頂端。 */
const KNOWN_ERROR_PREFIXES = [
  "stages.segment.",
  "stages.correct.",
  "stages.translate.",
  "timing.split_timing",
  "language.caption_priority",
  "language.allow_platform_translated",
  "language.targets",
  "language.variant_strategy",
  "language.fallback",
  "stages.frames.",
  "stages.note.",
  "outputs",
];

interface StepCtx {
  draft: Config;
  update: (path: string, value: unknown) => void;
  errors: FieldErrors;
  prompts: { id: string; kind: string; default_version: number; latest_version: number }[];
  styles: string[];
  templates: { id: string; name: string }[];
}

type TFn = ReturnType<typeof useTranslation>["t"];

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function inheritOptions(t: TFn, values: readonly string[], prefix: string) {
  return [
    { value: "", label: t("profiles.inherit") },
    ...values.map((v) => ({ value: v, label: t(`${prefix}.${v}`) })),
  ];
}

function outputsOf(doc: Config): Record<string, unknown>[] {
  const o = doc.outputs;
  return Array.isArray(o) ? (o.filter(isObject) as Record<string, unknown>[]) : [];
}

function FlowBar({ draft }: { draft: Config }) {
  const { t } = useTranslation();
  const steps: { key: string; off: boolean }[] = [
    { key: "transcript", off: false },
    ...LLM_STAGES.map((s) => ({ key: s, off: stageExecutor(draft, s) === "off" })),
    { key: "publish", off: outputsOf(draft).length === 0 },
  ];
  return (
    <ol className="flow" aria-label={t("profiles.flow")}>
      {steps.map((s) => (
        <li key={s.key} className={s.off ? "flow__step flow__step--off" : "flow__step"}>
          {t(`stage.${s.key}`)}
          {s.off && <span className="visually-hidden">{t("profiles.flowOff")}</span>}
        </li>
      ))}
    </ol>
  );
}

function Step({
  n,
  title,
  off,
  children,
}: {
  n: number;
  title: string;
  off?: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const hid = useId();
  return (
    <li className={off ? "step step--off" : "step"} aria-labelledby={hid}>
      <h2 className="step__title" id={hid}>
        <span className="step__num num" aria-hidden="true">
          {n}
        </span>
        {title}
        {off && <span className="tag">{t("executor.off")}</span>}
      </h2>
      <div className="step__body">{children}</div>
    </li>
  );
}

function LlmStageFields({ stage, draft, update, errors, prompts }: StepCtx & { stage: LlmStage }) {
  const { t } = useTranslation();
  const listId = useId();
  const base = `stages.${stage}`;
  const written = isObject(getPath(draft, base));
  const exec = stageExecutor(draft, stage);
  const ref = str(getPath(draft, `${base}.prompt`));
  const parsed = ref ? parsePromptRef(ref) : undefined;
  const candidates = prompts.filter((p) => p.kind === stage);
  const chosen = parsed && candidates.find((p) => p.id === parsed.id);
  const promptOptions = [
    { value: "", label: t("profiles.promptInherit", { id: `${stage}-default` }) },
    ...candidates.map((p) => ({ value: p.id, label: p.id })),
  ];
  if (parsed && !chosen) promptOptions.push({ value: parsed.id, label: parsed.id });
  const versionOptions = [
    {
      value: "",
      label: chosen
        ? t("profiles.versionFollow", { version: chosen.default_version })
        : t("profiles.versionFollowUnknown"),
    },
    ...Array.from({ length: chosen?.latest_version ?? parsed?.version ?? 0 }, (_, i) => ({
      value: String(i + 1),
      label: t("profiles.versionPinned", { version: i + 1 }),
    })),
  ];
  return (
    <div className="form-grid">
      <SelectField
        label={t("profiles.field.executor")}
        value={written ? (exec ?? "") : ""}
        onChange={(e) => {
          const v = e.target.value;
          if (!v) update(base, undefined);
          else update(`${base}.executor`, v);
        }}
        options={[
          { value: "", label: t("profiles.inherit") },
          ...stageExecutors(stage).map((x) => ({ value: x, label: t(`executor.${x}`) })),
        ]}
        error={errors[`${base}.executor`]}
      />
      {exec === "api" && (
        <>
          <TextField
            label={t("profiles.field.provider")}
            value={str(getPath(draft, `${base}.provider`))}
            list={listId}
            onChange={(e) => update(`${base}.provider`, e.target.value.trim() || undefined)}
            error={errors[`${base}.provider`]}
            autoComplete="off"
          />
          <datalist id={listId}>
            {knownProviders(stage).map((p) => (
              <option key={p} value={p} />
            ))}
          </datalist>
          <TextField
            label={t("profiles.field.model")}
            value={str(getPath(draft, `${base}.model`))}
            onChange={(e) => update(`${base}.model`, e.target.value.trim() || undefined)}
            error={errors[`${base}.model`]}
            autoComplete="off"
          />
        </>
      )}
      {(exec === "api" || exec === "agent") && (
        <>
          <SelectField
            label={t("profiles.field.prompt")}
            value={parsed?.id ?? ""}
            onChange={(e) => update(`${base}.prompt`, e.target.value || undefined)}
            options={promptOptions}
            error={errors[`${base}.prompt`]}
          />
          {parsed && (
            <SelectField
              label={t("profiles.field.promptVersion")}
              value={parsed.version ? String(parsed.version) : ""}
              onChange={(e) =>
                update(
                  `${base}.prompt`,
                  e.target.value ? `${parsed.id}@${e.target.value}` : parsed.id,
                )
              }
              options={versionOptions}
              hint={t("profiles.field.promptVersionHint")}
            />
          )}
        </>
      )}
      {!written && <p className="secondary step__note">{t("profiles.stageInheritNote")}</p>}
    </div>
  );
}

/** 筆記步驟的專屬欄位（NOTE、GEM 的介面）：模板、引擎、筆記語言、附上譯文。只在步驟寫進處理方案後顯示。 */
function NoteFields({ draft, update, errors, templates }: StepCtx) {
  const { t } = useTranslation();
  const tpl = str(getPath(draft, P.noteTemplate));
  const engine = str(getPath(draft, P.noteEngine));
  const incl = getPath(draft, P.noteIncludeTranslation);
  const engineOptions = inheritOptions(t, NOTE_ENGINES, "profiles.noteEngine");
  // 下拉選的是模板 id（跟隨最新版本）；固定版本（<id>@N，同 prompt 的引用）或清單中沒有的值原樣列為一個選項
  const templateOptions = [
    { value: "", label: t("profiles.inherit") },
    ...templates.map((x) => ({
      value: x.id,
      label:
        x.name && x.name !== x.id ? t("profiles.templateOption", { name: x.name, id: x.id }) : x.id,
    })),
  ];
  if (tpl && !templates.some((x) => x.id === tpl)) templateOptions.push({ value: tpl, label: tpl });
  return (
    <div className="form-grid">
      <SelectField
        label={t("profiles.field.noteTemplate")}
        value={tpl}
        onChange={(e) => update(P.noteTemplate, e.target.value || undefined)}
        options={templateOptions}
        hint={t("profiles.field.noteTemplateHint")}
        error={errors[P.noteTemplate]}
      />
      <SelectField
        label={t("profiles.field.noteEngine")}
        value={engine}
        onChange={(e) => update(P.noteEngine, e.target.value || undefined)}
        options={engineOptions}
        hint={t("profiles.field.noteEngineHint")}
        error={errors[P.noteEngine]}
      />
      <TextField
        label={t("profiles.field.noteLang")}
        hint={t("profiles.field.noteLangHint")}
        value={str(getPath(draft, P.noteLang))}
        onChange={(e) => update(P.noteLang, e.target.value.trim() || undefined)}
        error={errors[P.noteLang]}
        autoComplete="off"
        spellCheck={false}
      />
      <SelectField
        label={t("profiles.field.noteIncludeTranslation")}
        value={incl === true ? TRI.yes : incl === false ? TRI.no : TRI.inherit}
        onChange={(e) =>
          update(
            P.noteIncludeTranslation,
            e.target.value === "" ? undefined : e.target.value === TRI.yes,
          )
        }
        options={[
          { value: TRI.inherit, label: t("profiles.inherit") },
          { value: TRI.yes, label: t("profiles.noteIncludeYes") },
          { value: TRI.no, label: t("profiles.noteIncludeNo") },
        ]}
        error={errors[P.noteIncludeTranslation]}
      />
    </div>
  );
}

function NumberField({
  label,
  hint,
  path,
  min,
  max,
  step,
  draft,
  update,
  errors,
}: StepCtx & {
  label: string;
  hint?: string;
  path: string;
  min: number;
  max: number;
  step: number;
}) {
  const v = getPath(draft, path);
  const [text, setText] = useState(typeof v === "number" ? String(v) : "");
  return (
    <TextField
      label={label}
      hint={hint}
      type="number"
      min={min}
      max={max}
      step={step}
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        const n = e.target.value.trim() === "" ? undefined : Number(e.target.value);
        update(path, n === undefined || Number.isNaN(n) ? undefined : n);
      }}
      error={errors[path]}
    />
  );
}

/** 字串陣列（以逗號或空白分隔）。空白代表沿用全域設定。 */
function ListField({
  label,
  hint,
  path,
  draft,
  update,
  errors,
}: StepCtx & { label: string; hint?: string; path: string }) {
  const v = getPath(draft, path);
  // 放棄修改、重新載入時整個表單重新掛載（formKey），所以文字只在這裡初始化
  const [text, setText] = useState(Array.isArray(v) ? v.join(", ") : "");
  const err = Object.entries(errors).find(([p]) => p === path || p.startsWith(`${path}.`));
  return (
    <TextField
      label={label}
      hint={hint}
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        const list = splitList(e.target.value);
        update(path, list.length ? list : undefined);
      }}
      error={err?.[1]}
      autoComplete="off"
      spellCheck={false}
    />
  );
}

function SourceStep({ draft, update, errors }: StepCtx) {
  const { t } = useTranslation();
  const raw = getPath(draft, "language.caption_priority");
  const list = Array.isArray(raw)
    ? (raw.filter(isObject) as { lang?: string; kind?: string }[])
    : null;
  const allow = getPath(draft, "language.allow_platform_translated");
  const setList = (next: { lang?: string; kind?: string }[] | undefined) =>
    update("language.caption_priority", next);
  return (
    <div className="form-stack">
      <fieldset className="plain-fieldset">
        <legend className="field__label">{t("profiles.field.captionPriority")}</legend>
        {list === null ? (
          <p className="secondary">
            {t("profiles.captionInherit")}{" "}
            <Button variant="text" onClick={() => setList([NEW_CAPTION_FIRST])}>
              {t("profiles.captionCustomize")}
            </Button>
          </p>
        ) : (
          <>
            <ol className="caption-list">
              {list.map((c, i) => (
                <li key={i} className="caption-list__row">
                  <TextField
                    label={t("profiles.field.captionLang", { n: i + 1 })}
                    value={c.lang ?? ""}
                    onChange={(e) =>
                      setList(list.map((x, j) => (j === i ? { ...x, lang: e.target.value } : x)))
                    }
                    error={errors[`language.caption_priority.${i}.lang`]}
                    autoComplete="off"
                  />
                  <SelectField
                    label={t("profiles.field.captionKind", { n: i + 1 })}
                    value={c.kind ?? CAPTION_KINDS[0]}
                    onChange={(e) =>
                      setList(list.map((x, j) => (j === i ? { ...x, kind: e.target.value } : x)))
                    }
                    options={CAPTION_KINDS.map((k) => ({
                      value: k,
                      label: t(`profiles.captionKind.${k}`),
                    }))}
                    error={errors[`language.caption_priority.${i}.kind`]}
                  />
                  <Button
                    variant="text"
                    disabled={i === 0}
                    aria-label={t("profiles.moveUp", { n: i + 1 })}
                    onClick={() => {
                      const next = [...list];
                      [next[i - 1], next[i]] = [next[i]!, next[i - 1]!];
                      setList(next);
                    }}
                  >
                    {t("profiles.up")}
                  </Button>
                  <Button
                    variant="text"
                    aria-label={t("profiles.remove", { n: i + 1 })}
                    onClick={() => {
                      const next = list.filter((_, j) => j !== i);
                      setList(next.length ? next : undefined);
                    }}
                  >
                    {t("profiles.removeShort")}
                  </Button>
                </li>
              ))}
            </ol>
            <Button variant="text" onClick={() => setList([...list, NEW_CAPTION_NEXT])}>
              {t("profiles.captionAdd")}
            </Button>
          </>
        )}
        {errors["language.caption_priority"] && (
          <p className="field__error">{errors["language.caption_priority"]}</p>
        )}
      </fieldset>
      <FallbackField draft={draft} update={update} errors={errors} />
      <SelectField
        label={t("profiles.field.allowPlatformTranslated")}
        value={allow === true ? TRI.yes : allow === false ? TRI.no : TRI.inherit}
        onChange={(e) =>
          update(
            "language.allow_platform_translated",
            e.target.value === "" ? undefined : e.target.value === "yes",
          )
        }
        options={[
          { value: TRI.inherit, label: t("profiles.inherit") },
          { value: TRI.yes, label: t("profiles.use") },
          { value: TRI.no, label: t("profiles.notUse") },
        ]}
        error={errors["language.allow_platform_translated"]}
      />
    </div>
  );
}

/**
 * 平台字幕都不符合時改用的來源（language.fallback，例如 supadata、groq）。list 是整個取代，而且空陣列有意義
 * （不用後備、只用平台字幕，10.27 第 7 點），所以分成三種：沿用全域設定（不寫）、不用後備（[]）、自訂順序。
 * 自訂順序的文字是空的時不寫（等於沿用），說明會寫明。
 */
function FallbackField({ draft, update, errors }: Pick<StepCtx, "draft" | "update" | "errors">) {
  const { t } = useTranslation();
  const v = getPath(draft, P.fallback);
  const list = Array.isArray(v) ? (v as string[]) : undefined;
  const [mode, setMode] = useState<FallbackMode>(
    list === undefined
      ? FALLBACK_MODE.inherit
      : list.length === 0
        ? FALLBACK_MODE.none
        : FALLBACK_MODE.custom,
  );
  const [text, setText] = useState(list?.join(", ") ?? "");
  const err = Object.entries(errors).find(
    ([p]) => p === P.fallback || p.startsWith(`${P.fallback}.`),
  );
  return (
    <>
      <SelectField
        label={t("profiles.field.fallback")}
        value={mode}
        onChange={(e) => {
          const m = e.target.value as FallbackMode;
          setMode(m);
          if (m === FALLBACK_MODE.inherit) update(P.fallback, undefined);
          else if (m === FALLBACK_MODE.none) update(P.fallback, []);
          else {
            const l = splitList(text);
            update(P.fallback, l.length ? l : undefined);
          }
        }}
        options={[
          { value: FALLBACK_MODE.inherit, label: t("profiles.inherit") },
          { value: FALLBACK_MODE.none, label: t("profiles.fallbackNone") },
          { value: FALLBACK_MODE.custom, label: t("profiles.fallbackCustom") },
        ]}
        error={mode === FALLBACK_MODE.custom ? undefined : err?.[1]}
      />
      {mode === FALLBACK_MODE.custom && (
        <TextField
          label={t("profiles.field.fallbackList")}
          hint={t("profiles.field.fallbackListHint")}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            const l = splitList(e.target.value);
            update(P.fallback, l.length ? l : undefined);
          }}
          error={err?.[1]}
          autoComplete="off"
          spellCheck={false}
        />
      )}
    </>
  );
}

function OutputsStep({ draft, update, errors, styles }: StepCtx) {
  const { t } = useTranslation();
  const outputs = outputsOf(draft);
  // vault 與 hook 只能選用設定檔定義的 id（VAULT-R1、R10）
  const vaults = useVaults();
  const hooks = useHooks();
  const vaultIds = (vaults.data?.vaults ?? []).map((v) => v.id);
  const hookIds = (hooks.data?.hooks ?? []).map((h) => h.id);
  const setOutputs = (next: Record<string, unknown>[]) =>
    update("outputs", next.length ? next : undefined);
  const setAt = (i: number, key: string, value: unknown) =>
    setOutputs(
      outputs.map((o, j) => {
        if (j !== i) return o;
        // 換目標時只留下新目標認得的欄位（sidecar 不可以有 path；vault 沒有 path、style）
        if (key === "target") return retarget(o, String(value), vaultIds);
        const n = { ...o };
        if (value === undefined) delete n[key];
        else n[key] = value;
        return n;
      }),
    );
  return (
    <div className="form-stack">
      {outputs.length === 0 && <p className="secondary">{t("profiles.outputsEmpty")}</p>}
      {errors.outputs && <p className="field__error">{errors.outputs}</p>}
      {outputs.map((o, i) => {
        const p = `outputs.${i}`;
        const formats = Array.isArray(o.formats) ? (o.formats as string[]) : [];
        const isVault = o.target === "vault";
        const chosenHooks = Array.isArray(o.hooks) ? (o.hooks as string[]) : [];
        return (
          <fieldset key={i} className="output-card">
            <legend className="output-card__title">{t("profiles.outputN", { n: i + 1 })}</legend>
            <div className="form-grid">
              <SelectField
                label={t("profiles.field.target")}
                value={str(o.target)}
                onChange={(e) => setAt(i, "target", e.target.value)}
                options={OUTPUT_TARGETS.map((x) => ({
                  value: x,
                  label: t(`profiles.target.${x}`),
                }))}
                error={errors[`${p}.target`]}
              />
              {o.target === "folder" && (
                <TextField
                  label={t("profiles.field.path")}
                  value={str(o.path)}
                  onChange={(e) => setAt(i, "path", e.target.value)}
                  hint={t("profiles.field.pathHint")}
                  error={errors[`${p}.path`]}
                  spellCheck={false}
                />
              )}
              {isVault && (
                <SelectField
                  label={t("profiles.field.vault")}
                  value={str(o.vault)}
                  onChange={(e) => setAt(i, "vault", e.target.value || undefined)}
                  hint={vaultIds.length ? t("profiles.field.vaultHint") : t("profiles.noVaults")}
                  options={[
                    ...(str(o.vault) ? [] : [{ value: "", label: t("profiles.vaultPick") }]),
                    ...vaultIds.map((v) => ({ value: v, label: v })),
                    ...(str(o.vault) && !vaultIds.includes(str(o.vault))
                      ? [{ value: str(o.vault), label: str(o.vault) }]
                      : []),
                  ]}
                  error={errors[`${p}.vault`]}
                />
              )}
              {isVault && (
                <TextField
                  label={t("profiles.field.vaultFolder")}
                  value={str(o.folder)}
                  onChange={(e) => setAt(i, "folder", e.target.value.trim() || undefined)}
                  hint={t("profiles.field.vaultFolderHint")}
                  error={errors[`${p}.folder`]}
                  spellCheck={false}
                />
              )}
              {!isVault && (
                <SelectField
                  label={t("profiles.field.style")}
                  value={str(o.style)}
                  onChange={(e) => setAt(i, "style", e.target.value || undefined)}
                  options={[
                    { value: "", label: t("profiles.styleDefault") },
                    ...styles.map((s) => ({ value: s, label: s })),
                    ...(str(o.style) && !styles.includes(str(o.style))
                      ? [{ value: str(o.style), label: str(o.style) }]
                      : []),
                  ]}
                  error={errors[`${p}.style`]}
                />
              )}
            </div>
            <SelectField
              label={t("profiles.field.onUserChange")}
              value={str(o.on_user_change)}
              onChange={(e) => setAt(i, "on_user_change", e.target.value || undefined)}
              options={[
                { value: "", label: t("profiles.onUserChange.default") },
                ...ON_USER_CHANGE.map((x) => ({
                  value: x,
                  label: t(`profiles.onUserChange.${x}`),
                })),
              ]}
              hint={t("profiles.field.onUserChangeHint")}
              error={errors[`${p}.on_user_change`]}
            />
            <fieldset className="plain-fieldset">
              <legend className="field__label">{t("profiles.field.formats")}</legend>
              <div className="check-row">
                {OUTPUT_FORMATS.map((f) => (
                  <label key={f} className="check">
                    <input
                      type="checkbox"
                      checked={formats.includes(f)}
                      onChange={(e) => {
                        const next = e.target.checked
                          ? [...formats, f]
                          : formats.filter((x) => x !== f);
                        // vault 的 formats 可以是空陣列：只寫筆記與圖片（VAULT-R5）
                        setAt(i, "formats", next.length || isVault ? next : undefined);
                      }}
                    />
                    {t(`profiles.format.${f}`)}
                  </label>
                ))}
              </div>
              {(errors[`${p}.formats`] ??
                Object.entries(errors).find(([k]) => k.startsWith(`${p}.formats.`))?.[1]) && (
                <p className="field__error">
                  {errors[`${p}.formats`] ??
                    Object.entries(errors).find(([k]) => k.startsWith(`${p}.formats.`))?.[1]}
                </p>
              )}
            </fieldset>
            {isVault && (
              <fieldset className="plain-fieldset">
                <legend className="field__label">{t("profiles.field.hooks")}</legend>
                <p className="field__hint">
                  {hookIds.length ? t("profiles.field.hooksHint") : t("profiles.noHooks")}
                </p>
                <div className="check-row">
                  {[...new Set([...hookIds, ...chosenHooks])].map((h) => (
                    <label key={h} className="check">
                      <input
                        type="checkbox"
                        checked={chosenHooks.includes(h)}
                        onChange={(e) => {
                          const next = e.target.checked
                            ? [...chosenHooks, h]
                            : chosenHooks.filter((x) => x !== h);
                          setAt(i, "hooks", next.length ? next : undefined);
                        }}
                      />
                      <code>{h}</code>
                    </label>
                  ))}
                </div>
                {Object.entries(errors).find(([k]) => k.startsWith(`${p}.hooks`))?.[1] && (
                  <p className="field__error">
                    {Object.entries(errors).find(([k]) => k.startsWith(`${p}.hooks`))?.[1]}
                  </p>
                )}
              </fieldset>
            )}
            <Button variant="text" onClick={() => setOutputs(outputs.filter((_, j) => j !== i))}>
              {t("profiles.outputRemove", { n: i + 1 })}
            </Button>
          </fieldset>
        );
      })}
      <Button variant="text" onClick={() => setOutputs([...outputs, NEW_OUTPUT])}>
        {t("profiles.outputAdd")}
      </Button>
    </div>
  );
}
