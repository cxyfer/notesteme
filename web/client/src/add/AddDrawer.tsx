// 「新增」抽屜（docs/ui/pages.md §2、ING-R1～R6、R9）：
//
//   1. 貼上網址 → POST /api/resolve 辨識（延遲 400ms，不建立影音）→ 顯示預覽：
//      影片（Bilibili 沒有指定分P 時可以選「全部」或指定編號）、播放清單（整份新增）、
//      頻道（不能直接新增，提示建立訂閱）、Podcast feed（勾選要新增的集，ING-R6）。
//   2. 或選擇本機檔案（multipart POST /api/items，一次一個檔案）。
//   3. 選擇處理方案（GET /api/profiles），按「開始處理」→ POST /api/items。
//   4. 成功：關閉抽屜，toast「已開始處理 N 部」附「查看」；全部都已在媒體庫中時留在抽屜，提供「打開」。
//      錯誤依 code 顯示並給修正動作（例如 in_trash 給「還原」）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../api/client";
import { isApiError } from "../api/errors";
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
import { formatDate, formatNumber, formatTimecode } from "../i18n";
import { isHttpUrl, parseParts } from "./url";

type Resolution = components["schemas"]["Resolution"];
type IngestedItem = components["schemas"]["IngestedItem"];
type CreateBody = components["schemas"]["CreateItemInputBody"];

const RESOLVE_DELAY_MS = 400;

export interface AddDrawerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initialUrl?: string;
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

export function AddDrawer({ open, onOpenChange, initialUrl = "" }: AddDrawerProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const formId = useId();
  const urlRef = useRef<HTMLInputElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const [url, setUrl] = useState(initialUrl);
  const [file, setFile] = useState<File | null>(null);
  const [partsMode, setPartsMode] = useState<"all" | "some">("all");
  const [partsText, setPartsText] = useState("");
  const [episodes, setEpisodes] = useState<Set<string>>(new Set());
  const [profile, setProfile] = useState<string | undefined>();
  const [existing, setExisting] = useState<IngestedItem[]>([]);

  // ---- 辨識 ----
  const trimmed = url.trim();
  const debounced = useDebouncedValue(trimmed, RESOLVE_DELAY_MS);
  // 帶入的網址（貼上）不用等
  const target = trimmed === initialUrl.trim() ? trimmed : debounced;
  const canResolve = !file && isHttpUrl(target);
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

  // ---- 送出的內容 ----
  const isBiliAllParts = res?.platform === "bilibili" && res.kind === "video" && !res.part;
  const parts = isBiliAllParts && partsMode === "some" ? parseParts(partsText) : undefined;
  const partsInvalid = isBiliAllParts && partsMode === "some" && parts === undefined;
  const isFeed = res?.kind === "feed";
  const blockedReason: string | undefined = file
    ? undefined
    : !trimmed
      ? t("add.needInput")
      : !isHttpUrl(trimmed)
        ? t("add.invalidUrl")
        : res?.subscribable
          ? t("add.channelHint")
          : isFeed && episodes.size === 0
            ? t("add.pickEpisodes")
            : partsInvalid
              ? t("add.partsInvalid")
              : undefined;
  const ready = file !== null || (res !== undefined && !blockedReason);

  const submit = useMutation({
    mutationFn: async () => {
      if (file) {
        const fd = new FormData();
        fd.set("file", file);
        if (chosenProfile) fd.set("profile", chosenProfile);
        return api.post("/api/items", { body: fd });
      }
      const body: CreateBody = { url: res?.url || trimmed };
      if (chosenProfile) body.profile = chosenProfile;
      if (parts) body.parts = parts;
      if (isFeed) body.episodes = [...episodes];
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

  function openItem(id: string) {
    onOpenChange(false);
    void navigate({ to: "/items/$id", params: { id } });
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!ready || submit.isPending) return;
    setExisting([]);
    submit.mutate();
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
          disabled={!ready || submit.isPending}
          aria-describedby={blockedReason && !file ? `${formId}-blocked` : undefined}
        >
          {submit.isPending ? t("add.submitting") : t("add.submit")}
        </Button>
      }
    >
      <form id={formId} className="add" onSubmit={onSubmit} noValidate>
        <div className="field">
          <label className="field__label" htmlFor={`${formId}-url`}>
            {t("add.url")}
          </label>
          <input
            ref={urlRef}
            id={`${formId}-url`}
            className="field__input"
            type="url"
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            placeholder={t("add.urlPlaceholder")}
            value={url}
            disabled={file !== null}
            onChange={(e) => {
              setUrl(e.target.value);
              setExisting([]);
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
            onChange={(e) => setProfile(e.target.value || undefined)}
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

        {blockedReason && !file && trimmed && !resolving && (
          <p id={`${formId}-blocked`} className="add__blocked secondary">
            {blockedReason}
          </p>
        )}

        {existing.length > 0 && (
          <div className="add__existing" role="status">
            <p>{t("add.alreadyInLibrary", { count: existing.length })}</p>
            <ul>
              {existing.map((it) => (
                <li key={it.id}>
                  <span lang={it.original_lang || undefined}>{it.title || it.ref}</span>
                  <Button variant="text" onClick={() => openItem(it.id)}>
                    {it.in_trash ? t("add.inTrash") : t("add.openItem")}
                  </Button>
                </li>
              ))}
            </ul>
          </div>
        )}

        {submit.isError && (
          <ErrorCallout error={submit.error} actions={errorActions(submit.error)} />
        )}
        {restore.isError && <ErrorCallout error={restore.error} />}
      </form>
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
}: {
  res: Resolution;
  partsMode: "all" | "some";
  setPartsMode: (m: "all" | "some") => void;
  partsText: string;
  setPartsText: (s: string) => void;
  partsInvalid: boolean;
  episodes: Set<string>;
  setEpisodes: (s: Set<string>) => void;
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
      {res.subscribable && <p>{t("add.channelHint")}</p>}

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
