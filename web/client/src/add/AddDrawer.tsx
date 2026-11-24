// 「新增」抽屜（docs/ui/pages.md §2、ING-R1～R6、R9）：
//
//   1. 貼上網址（一行一個；兩行以上走 MultiAdd：逐行辨識、預估，送出時逐行新增）
//      → POST /api/resolve 辨識（延遲 400ms，不建立影音）→ 顯示預覽：
//      影片（Bilibili 沒有指定分P 時可以選「全部」或指定編號）、播放清單（整份新增）、
//      頻道（不能直接新增，改為提供「建立訂閱」：打開 /subscriptions?subscribe=<網址>，SUB-R1）、
//      Podcast feed（勾選要新增的集，ING-R6；也可以改成建立訂閱）。
//      辨識成功、不是頻道時另外呼叫 POST /api/estimate 顯示預估花費（ENTRY-R2 的書籤與分享也會看到）；
//      估算可能呼叫 yt-dlp，所以延遲送出、不重試，失敗也不擋「開始處理」。
//   2. 或選擇本機檔案（multipart POST /api/items，一次一個檔案）；把檔案拖進視窗時由 AddContext 帶入（initialFile）。
//   3. 選擇處理方案（GET /api/profiles），可以展開「調整這次的設定」（RunOverrides，單次覆寫只影響這一次，ING-R7），
//      按「開始處理」→ POST /api/items（overrides 也送給預估）。
//   4. 已經處理過的影音在辨識（預估）後就標示「已在媒體庫中」，提供「打開」與「重新處理」（AlreadyInLibrary）。
//      成功：關閉抽屜，toast「已開始處理 N 部」附「查看」；全部都已在媒體庫中時留在抽屜，提供「打開」。
//      錯誤依 code 顯示並給修正動作（例如 in_trash 給「還原」）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../api/client";
import { describeError } from "../api/describeError";
import { isApiError } from "../api/errors";
import { fieldErrorsOf } from "../api/fieldErrors";
import { queryKeys } from "../api/queryKeys";
import type { components } from "../api/schema";
import {
  Button,
  Drawer,
  ErrorCallout,
  Spinner,
  Thumbnail,
  useToast,
  type ErrorAction,
} from "../components";
import { formatDate, formatNumber, formatTimecode, formatUsd } from "../i18n";
import {
  buildOverrides,
  EMPTY_RUN_OVERRIDES,
  RunOverrides,
  useRunDefaults,
  type RunOverridesValue,
} from "./RunOverrides";
import { AlreadyInLibrary, type LibraryEntry } from "./AlreadyInLibrary";
import { MAX_LINES, MultiLines, uniqueLines, useMultiLines, type LineState } from "./MultiAdd";
import { isHttpUrl, parseParts } from "./url";

type Resolution = components["schemas"]["Resolution"];
type IngestedItem = components["schemas"]["IngestedItem"];
type CreateBody = components["schemas"]["CreateItemInputBody"];
type EstimateBody = components["schemas"]["EstimateInputBody"];
type EstimateResult = components["schemas"]["EstimateResult"];
type OkLine = Extract<LineState, { kind: "ok" }>;
/** 多行送出時一行的結果 */
type LineResult = { url: string; items: IngestedItem[] } | { url: string; error: unknown };

const RESOLVE_DELAY_MS = 400;

export interface AddDrawerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initialUrl?: string;
  /** 帶入要上傳的檔案（把檔案拖進視窗） */
  initialFile?: File;
  /** 抽屜上方的提示（例如書籤或分享帶進來的網址無效，ENTRY-R2、R4） */
  notice?: string;
}

function useDebouncedValue(value: string, ms: number) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

/** 網址錯誤（要改網址才能修正）的 code */
const URL_CODES = new Set([
  "unrecognized_url",
  "unsupported_scheme",
  "blocked_address",
  "too_many_redirects",
  "response_too_large",
  "channel_not_ingestable",
]);
/** 檔案錯誤（要換檔案）的 code */
const FILE_CODES = new Set(["upload_too_large", "unsupported_media"]);

export function AddDrawer({
  open,
  onOpenChange,
  initialUrl = "",
  initialFile,
  notice,
}: AddDrawerProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const formId = useId();
  const urlRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const [text, setText] = useState(initialUrl);
  /** 上次多行送出時失敗的行（網址 → 錯誤） */
  const [lineErrors, setLineErrors] = useState<ReadonlyMap<string, unknown>>(new Map());
  const [file, setFile] = useState<File | null>(initialFile ?? null);
  const [partsMode, setPartsMode] = useState<"all" | "some">("all");
  const [partsText, setPartsText] = useState("");
  const [episodes, setEpisodes] = useState<Set<string>>(new Set());
  const [profile, setProfile] = useState<string | undefined>();
  const [existing, setExisting] = useState<IngestedItem[]>([]);
  const [runOpen, setRunOpen] = useState(false);
  const [run, setRun] = useState<RunOverridesValue>(EMPTY_RUN_OVERRIDES);

  // ---- 辨識 ----
  const lines = uniqueLines(text);
  const multi = !file && lines.length > 1;
  // 一行（或空的）時沿用原本的單一網址流程
  const trimmed = multi ? "" : (lines[0] ?? "");
  const debounced = useDebouncedValue(trimmed, RESOLVE_DELAY_MS);
  // 帶入的網址（貼上）不用等
  const target = trimmed === initialUrl.trim() ? trimmed : debounced;
  const canResolve = !file && !multi && isHttpUrl(target);
  const resolve = useQuery({
    queryKey: ["resolve", target],
    queryFn: ({ signal }) => api.post("/api/resolve", { body: { url: target }, signal }),
    enabled: open && canResolve,
    retry: false,
    staleTime: 60_000,
  });
  const res: Resolution | undefined = canResolve && target === trimmed ? resolve.data : undefined;
  const resolving = canResolve && (resolve.isFetching || target !== trimmed);

  // ---- 處理方案 ----
  const profiles = useQuery({
    queryKey: queryKeys.profiles,
    queryFn: ({ signal }) => api.get("/api/profiles", { signal }),
    enabled: open,
    staleTime: 60_000,
  });
  const profileList = profiles.data?.profiles ?? [];
  const defaultProfile = profileList.find((p) => p.default)?.name;
  const chosenProfile = profile ?? defaultProfile;
  const profileConfig = profileList.find((p) => p.name === chosenProfile)?.config;

  // ---- 這次的設定（單次覆寫） ----
  const runDefaults = useRunDefaults(profileConfig, open && runOpen);
  const overrides = buildOverrides(run, runDefaults.sources);

  // ---- 多個網址（一行一個） ----
  const debouncedLines = uniqueLines(useDebouncedValue(text, RESOLVE_DELAY_MS));
  const lineStates = useMultiLines({
    lines: multi ? debouncedLines.slice(0, MAX_LINES) : [],
    enabled: open && multi,
    base: {
      ...(chosenProfile ? { profile: chosenProfile } : {}),
      ...(overrides ? { overrides } : {}),
    },
    estimateEnabled: open && multi && !profiles.isPending,
  });
  const extraLines = Math.max(0, lines.length - MAX_LINES);
  const linesCurrent =
    debouncedLines.length === lines.length && debouncedLines.every((l, i) => l === lines[i]);
  const okLines = lineStates.filter((s): s is OkLine => s.kind === "ok");
  const linesResolving = !linesCurrent || lineStates.some((s) => s.kind === "resolving");

  // ---- 送出的內容 ----
  const isBiliAllParts = res?.platform === "bilibili" && res.kind === "video" && !res.part;
  const parts = isBiliAllParts && partsMode === "some" ? parseParts(partsText) : undefined;
  const partsInvalid = isBiliAllParts && partsMode === "some" && parts === undefined;
  const isFeed = res?.kind === "feed";
  const blockedReason: string | undefined = file
    ? undefined
    : multi
      ? extraLines > 0
        ? t("add.lines.tooMany", { max: MAX_LINES, extra: extraLines })
        : !linesResolving && okLines.length === 0
          ? t("add.lines.noneReady")
          : undefined
      : !trimmed
        ? t("add.needInput")
        : !isHttpUrl(trimmed)
          ? t("add.invalidUrl")
          : res?.subscribable
            ? t("add.channelBlocked")
            : isFeed && episodes.size === 0
              ? t("add.pickEpisodes")
              : partsInvalid
                ? t("add.partsInvalid")
                : undefined;
  const ready =
    file !== null ||
    (multi
      ? !blockedReason && !linesResolving && okLines.length > 0
      : res !== undefined && !blockedReason);

  // ---- 預估花費（POST /api/estimate）：和送出的內容相同；勾選分P／集數時等停下來再送 ----
  const estimateBody: EstimateBody | undefined =
    !file && res && !res.subscribable && !partsInvalid && !(isFeed && episodes.size === 0)
      ? {
          url: res.url || trimmed,
          ...(chosenProfile ? { profile: chosenProfile } : {}),
          ...(parts ? { parts } : {}),
          ...(isFeed ? { episodes: [...episodes].sort() } : {}),
          ...(overrides ? { overrides } : {}),
        }
      : undefined;
  const estimateJson = estimateBody ? JSON.stringify(estimateBody) : "";
  const estimateKey = useDebouncedValue(estimateJson, RESOLVE_DELAY_MS);
  const estimate = useQuery({
    queryKey: ["estimate", estimateKey],
    queryFn: ({ signal }) =>
      api.post("/api/estimate", { body: JSON.parse(estimateKey) as EstimateBody, signal }),
    // 等處理方案清單讀完再送，避免先用「沒有指定」估一次
    enabled: open && estimateKey !== "" && !profiles.isPending,
    retry: false,
    staleTime: 60_000,
  });
  const estimateCurrent = estimateJson !== "" && estimateKey === estimateJson;

  // 辨識（預估）後就知道已在媒體庫中的影音（pages.md §2）；多行時合計每一行的
  const known: LibraryEntry[] = (
    multi
      ? okLines.flatMap((l) => l.estimate?.items ?? [])
      : estimateCurrent
        ? (estimate.data?.items ?? [])
        : []
  )
    .filter((i) => i.already_in_library)
    .map((i) => ({ ref: i.ref, title: i.title || i.ref }));

  function subscribeInstead() {
    const target = trimmed;
    onOpenChange(false);
    void navigate({ to: "/subscriptions", search: { subscribe: target } });
  }

  const submit = useMutation({
    mutationFn: async () => {
      if (file) {
        // 文字欄位放在檔案前面：後端邊讀邊驗證，處理方案有錯時不必等檔案傳完
        const fd = new FormData();
        if (chosenProfile) fd.set("profile", chosenProfile);
        if (overrides) fd.set("overrides", JSON.stringify(overrides));
        fd.set("file", file);
        return api.post("/api/items", { body: fd });
      }
      const body: CreateBody = { url: res?.url || trimmed };
      if (chosenProfile) body.profile = chosenProfile;
      if (parts) body.parts = parts;
      if (isFeed) body.episodes = [...episodes];
      if (overrides) body.overrides = overrides;
      return api.post("/api/items", { body });
    },
    onSuccess: (out) => {
      const items = out?.items ?? [];
      const created = items.filter((i) => !i.already_in_library && !i.in_trash);
      const already = items.filter((i) => i.already_in_library);
      if (created.length === 0) {
        // 全部都已在媒體庫中：留在抽屜，提供「打開」
        setExisting(already.length ? already : items);
        return;
      }
      onOpenChange(false);
      const first = created[0]!;
      toast.show({
        title:
          already.length > 0
            ? t("add.startedWithExisting", {
                count: created.length,
                existing: already.length,
              })
            : t("add.started", { count: created.length }),
        action: {
          label: t("add.view"),
          onClick: () =>
            void (created.length === 1
              ? navigate({ to: "/items/$id", params: { id: first.id } })
              : navigate({ to: "/library", search: {} })),
        },
      });
    },
  });

  const restore = useMutation({
    mutationFn: (id: string) =>
      api.post<"/api/trash/{id}/restore">(apiPath("/api/trash/{id}/restore", { id })),
    onSuccess: (_out, id) => {
      onOpenChange(false);
      void qc.invalidateQueries({ queryKey: queryKeys.items.all });
      toast.show({
        title: t("add.restored"),
        action: {
          label: t("add.openItem"),
          onClick: () => void navigate({ to: "/items/$id", params: { id } }),
        },
      });
    },
  });

  // 多個網址：依序逐行新增（一個失敗不影響其他行），結果合計在 toast
  const submitMany = useMutation({
    mutationFn: async (targets: OkLine[]): Promise<LineResult[]> => {
      const out: LineResult[] = [];
      for (const s of targets) {
        try {
          const r = await api.post("/api/items", { body: s.body as CreateBody });
          out.push({ url: s.url, items: r?.items ?? [] });
        } catch (error) {
          out.push({ url: s.url, error });
        }
      }
      return out;
    },
    onSuccess: (results) => {
      const items = results.flatMap((r) => ("items" in r ? r.items : []));
      const created = items.filter((i) => !i.already_in_library && !i.in_trash);
      const already = items.filter((i) => i.already_in_library).length;
      const failed = results.filter((r) => "error" in r);
      const summary = [
        created.length > 0 ? t("add.started", { count: created.length }) : "",
        already > 0 ? t("add.summary.existing", { count: already }) : "",
        failed.length > 0 ? t("add.summary.failed", { count: failed.length }) : "",
      ]
        .filter(Boolean)
        .join(t("add.summary.separator"));
      if (failed.length === 0) {
        onOpenChange(false);
      } else {
        // 留在抽屜：成功的行從輸入框移除，失敗的行就地標示（沒送出的行也留著）
        const done = new Set(results.filter((r) => "items" in r).map((r) => r.url));
        setText(lines.filter((l) => !done.has(l)).join("\n"));
        setLineErrors(new Map(failed.map((r) => [r.url, (r as { error: unknown }).error])));
      }
      const first = created[0];
      toast.show({
        title: summary,
        ...(first
          ? {
              action: {
                label: t("add.view"),
                onClick: () =>
                  void (created.length === 1
                    ? navigate({ to: "/items/$id", params: { id: first.id } })
                    : navigate({ to: "/library", search: {} })),
              },
            }
          : {}),
      });
    },
  });

  function openItem(id: string) {
    onOpenChange(false);
    void navigate({ to: "/items/$id", params: { id } });
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!ready || submit.isPending || submitMany.isPending) return;
    setExisting([]);
    setLineErrors(new Map());
    if (multi) submitMany.mutate(okLines);
    else submit.mutate();
  }

  function errorActions(err: unknown): ErrorAction[] {
    if (!isApiError(err)) return [];
    if (err.code === "in_trash" && err.body.item_id) {
      const id = err.body.item_id;
      return [{ label: t("add.restore"), primary: true, onClick: () => restore.mutate(id) }];
    }
    if (URL_CODES.has(err.code))
      return [
        {
          label: t("add.editUrl"),
          onClick: () => document.getElementById(`${formId}-url`)?.focus(),
        },
      ];
    if (FILE_CODES.has(err.code))
      return [
        {
          label: t("add.pickOtherFile"),
          onClick: () => document.getElementById(`${formId}-file`)?.click(),
        },
      ];
    if (err.status === 0 || err.status >= 500 || err.code === "too_many_uploads")
      return [{ label: t("common.retry"), onClick: () => submit.mutate() }];
    return [];
  }

  const sizeLabel = useMemo(
    () =>
      file ? t("add.fileSize", { size: formatNumber(Math.ceil(file.size / 1024 / 1024)) }) : "",
    [file, t],
  );

  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title={t("add.title")}
      description={t("add.description")}
      footer={
        <Button
          type="submit"
          form={formId}
          variant="primary"
          disabled={!ready || submit.isPending || submitMany.isPending}
          aria-describedby={blockedReason && !file ? `${formId}-blocked` : undefined}
        >
          {submit.isPending || submitMany.isPending ? t("add.submitting") : t("add.submit")}
        </Button>
      }
    >
      <form id={formId} className="add" onSubmit={onSubmit} noValidate>
        {notice && (
          <p className="callout add__notice" role="status" data-category="needs_action">
            {notice}
          </p>
        )}
        <div className="field">
          <label className="field__label" htmlFor={`${formId}-url`}>
            {t("add.url")}
          </label>
          <textarea
            ref={urlRef}
            id={`${formId}-url`}
            className="field__input add__urls"
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            rows={Math.min(Math.max(lines.length, 1), 6)}
            placeholder={t("add.urlPlaceholder")}
            value={text}
            disabled={file !== null}
            onChange={(e) => {
              setText(e.target.value);
              setExisting([]);
              setLineErrors(new Map());
              submit.reset();
            }}
            aria-describedby={`${formId}-url-hint`}
          />
          <span id={`${formId}-url-hint`} className="field__hint">
            {t("add.urlHint")}
          </span>
        </div>

        <div className="add__file">
          <input
            ref={fileRef}
            id={`${formId}-file`}
            className="visually-hidden"
            type="file"
            tabIndex={-1}
            aria-label={t("add.pickFile")}
            accept="audio/*,video/*,.mkv,.webm,.opus,.flac,.m4a"
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? null);
              setExisting([]);
              submit.reset();
            }}
          />
          {file ? (
            <p className="add__file-name">
              <span>{t("add.fileChosen", { name: file.name })}</span>
              <span className="secondary num">{sizeLabel}</span>
              <Button
                variant="text"
                onClick={() => {
                  setFile(null);
                  if (fileRef.current) fileRef.current.value = "";
                }}
              >
                {t("add.removeFile")}
              </Button>
            </p>
          ) : (
            <Button onClick={() => fileRef.current?.click()}>{t("add.pickFile")}</Button>
          )}
        </div>

        {multi && <MultiLines states={lineStates} errors={lineErrors} extra={extraLines} />}
        {/* 多行送出後只剩一行失敗時，錯誤仍顯示在單一網址的畫面 */}
        {!multi && !file && lineErrors.has(trimmed) && (
          <ErrorCallout error={lineErrors.get(trimmed)} />
        )}
        {!file && resolving && <Spinner label={t("add.resolving")} showLabel />}
        {!file && canResolve && resolve.isError && target === trimmed && (
          <ErrorCallout error={resolve.error} actions={errorActions(resolve.error)} />
        )}
        {!file && res && (
          <Preview
            res={res}
            partsMode={partsMode}
            setPartsMode={setPartsMode}
            partsText={partsText}
            setPartsText={setPartsText}
            partsInvalid={partsInvalid}
            episodes={episodes}
            setEpisodes={setEpisodes}
            onSubscribe={subscribeInstead}
          />
        )}
        {estimateCurrent && (
          <EstimateLine
            result={estimate.data}
            loading={estimate.isFetching}
            error={estimate.error}
          />
        )}

        <div className="field">
          <label className="field__label" htmlFor={`${formId}-profile`}>
            {t("add.profile")}
          </label>
          <select
            id={`${formId}-profile`}
            className="field__input"
            value={chosenProfile ?? ""}
            disabled={!profiles.isSuccess || profileList.length === 0}
            onChange={(e) => {
              setProfile(e.target.value || undefined);
              // 字幕來源的起始清單跟著處理方案：換方案時丟掉舊方案的清單，免得把新方案移除的來源整份送回去
              setRun((r) => ({ ...r, sources: undefined }));
            }}
          >
            {profileList.length === 0 && <option value="">{t("add.profileDefault")}</option>}
            {profileList.map((p) => (
              <option key={p.name} value={p.name}>
                {p.default ? t("add.profileIsDefault", { name: p.name }) : p.name}
              </option>
            ))}
          </select>
          {profiles.isError && <span className="field__hint">{t("add.profilesUnavailable")}</span>}
        </div>

        <RunOverrides
          open={runOpen}
          onToggle={setRunOpen}
          value={run}
          onChange={(v) => {
            setRun(v);
            submit.reset();
          }}
          defaults={runDefaults}
          errors={fieldErrorsOf(submit.error)}
        />

        {blockedReason && !file && (multi || trimmed) && !resolving && (
          <p id={`${formId}-blocked`} className="add__blocked secondary">
            {blockedReason}
          </p>
        )}

        {submit.isError && (
          <ErrorCallout error={submit.error} actions={errorActions(submit.error)} />
        )}
        {restore.isError && <ErrorCallout error={restore.error} />}
      </form>
      {/* 放在 form 之外：「重新處理」的對話框是 portal，但 React 的 submit 事件會沿元件樹冒泡到這個 form */}
      {existing.length > 0 ? (
        <AlreadyInLibrary
          entries={existing.map((it) => ({
            id: it.id,
            title: it.title || it.ref,
            lang: it.original_lang,
            inTrash: it.in_trash,
          }))}
          message={t("add.alreadyInLibrary", { count: existing.length })}
          onOpen={openItem}
        />
      ) : (
        known.length > 0 && (
          <AlreadyInLibrary
            entries={known}
            message={t("add.knownInLibrary", { count: known.length })}
            onOpen={openItem}
          />
        )
      )}
    </Drawer>
  );
}

function Preview({
  res,
  partsMode,
  setPartsMode,
  partsText,
  setPartsText,
  partsInvalid,
  episodes,
  setEpisodes,
  onSubscribe,
}: {
  res: Resolution;
  partsMode: "all" | "some";
  setPartsMode: (m: "all" | "some") => void;
  partsText: string;
  setPartsText: (s: string) => void;
  partsInvalid: boolean;
  episodes: Set<string>;
  setEpisodes: (s: Set<string>) => void;
  onSubscribe: () => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const platform = t(`platform.${res.platform}`, { defaultValue: res.platform });
  const kind = t(`add.kind.${res.kind}`, { platform, defaultValue: res.kind });
  const eps = res.episodes ?? [];
  return (
    <section className="add__preview" aria-labelledby={`${id}-kind`}>
      <div className="add__preview-head">
        <Thumbnail
          platform={res.platform}
          externalId={res.external_id}
          placeholder={platform}
          className="add__thumb"
        />
        <div>
          <h3 id={`${id}-kind`} className="add__kind">
            {kind}
          </h3>
          {res.title && <p className="add__title">{res.title}</p>}
          {res.external_id && <p className="secondary num">{res.external_id}</p>}
          {res.kind === "video" && res.part ? (
            <p className="secondary">{t("add.onlyPart", { part: res.part })}</p>
          ) : null}
        </div>
      </div>

      {res.requires_cookie && (
        <p className="add__warn">
          {t("add.requiresCookie")}{" "}
          <a href={res.settings_link?.startsWith("/") ? res.settings_link : "/settings/accounts"}>
            {t("add.cookieSettings")}
          </a>
        </p>
      )}

      {res.kind === "playlist" && <p>{t("add.playlistWhole")}</p>}
      {res.subscribable && (
        <div className="add__subscribe">
          <p>{t("add.channelHint")}</p>
          <Button variant="primary" onClick={onSubscribe}>
            {t("add.subscribe")}
          </Button>
        </div>
      )}
      {res.kind === "feed" && (
        <p className="add__subscribe">
          <span className="secondary">{t("add.feedSubscribeHint")}</span>{" "}
          <Button variant="text" onClick={onSubscribe}>
            {t("add.subscribe")}
          </Button>
        </p>
      )}

      {res.platform === "bilibili" && res.kind === "video" && !res.part && (
        <fieldset className="add__parts">
          <legend>{t("add.parts")}</legend>
          <label className="check">
            <input
              type="radio"
              name={`${id}-parts`}
              checked={partsMode === "all"}
              onChange={() => setPartsMode("all")}
            />
            <span>{t("add.partsAll")}</span>
          </label>
          <label className="check">
            <input
              type="radio"
              name={`${id}-parts`}
              checked={partsMode === "some"}
              onChange={() => setPartsMode("some")}
            />
            <span>{t("add.partsSome")}</span>
          </label>
          {partsMode === "some" && (
            <div className="field">
              <label className="field__label" htmlFor={`${id}-parts-text`}>
                {t("add.partsNumbers")}
              </label>
              <input
                id={`${id}-parts-text`}
                className="field__input"
                value={partsText}
                inputMode="numeric"
                aria-invalid={partsInvalid && partsText ? true : undefined}
                aria-describedby={`${id}-parts-hint`}
                onChange={(e) => setPartsText(e.target.value)}
              />
              <span id={`${id}-parts-hint`} className="field__hint">
                {t("add.partsHint")}
              </span>
            </div>
          )}
        </fieldset>
      )}

      {res.kind === "feed" && (
        <fieldset className="add__episodes">
          <legend>{t("add.episodes", { count: eps.length })}</legend>
          <div className="add__episodes-actions">
            <Button variant="text" onClick={() => setEpisodes(new Set(eps.map((e) => e.guid)))}>
              {t("add.selectAll")}
            </Button>
            <Button variant="text" onClick={() => setEpisodes(new Set())}>
              {t("add.selectNone")}
            </Button>
          </div>
          <ul className="add__episode-list">
            {eps.map((ep) => (
              <li key={ep.guid}>
                <label className="check">
                  <input
                    type="checkbox"
                    checked={episodes.has(ep.guid)}
                    onChange={() => {
                      const next = new Set(episodes);
                      if (next.has(ep.guid)) next.delete(ep.guid);
                      else next.add(ep.guid);
                      setEpisodes(next);
                    }}
                  />
                  <span>{ep.title || ep.guid}</span>
                  <span className="secondary num">
                    {[
                      ep.published_at ? formatDate(ep.published_at) : "",
                      ep.duration_s ? formatTimecode(ep.duration_s) : "",
                    ]
                      .filter(Boolean)
                      .join(t("common.listSeparator"))}
                  </span>
                </label>
              </li>
            ))}
          </ul>
        </fieldset>
      )}
    </section>
  );
}

/** 預估花費：只顯示已知金額；全部未知時寫明無法預估，不顯示 $0.00（同 UI-4 決定 4） */
function EstimateLine({
  result,
  loading,
  error,
}: {
  result: EstimateResult | undefined;
  loading: boolean;
  error: unknown;
}) {
  const { t } = useTranslation();
  if (loading) return <Spinner label={t("add.estimating")} showLabel />;
  if (error != null)
    return (
      <p className="add__estimate secondary">
        {t("add.estimateFailed", { reason: describeError(t, error) })}
      </p>
    );
  if (!result) return null;
  const items = result.items ?? [];
  const known = items.filter((i) => i.total_usd != null).length;
  const notes = [...new Set([...(result.notes ?? []), ...items.flatMap((i) => i.notes ?? [])])];
  return (
    <div className="add__estimate" role="status">
      <p>
        {known === 0
          ? t("add.estimateUnknown")
          : known < items.length
            ? t("add.estimatePartial", { usd: formatUsd(result.total_usd) })
            : t("add.estimate", { usd: formatUsd(result.total_usd) })}
      </p>
      {notes.length > 0 && (
        <ul className="add__estimate-notes secondary">
          {notes.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
