// 播放器包裝（LIB-R6）：同一個介面 seek(t)，播放位置用 onTime 回報。
//
//   - YouTube：嵌入 iframe（enablejsapi=1），用 postMessage 控制與讀取播放位置。不載入 IFrame API 的 script：
//     CSP 的 script-src 只有 'self'，frame-src 已開放 youtube.com／youtube-nocookie.com，不需要放寬（NFR-SEC-1）。
//     嵌入後 4 秒內沒有收到播放器的訊息，就降級成「更新 start 參數重新載入」。
//   - Bilibili：官方嵌入播放器沒有公開的控制介面，跳到某個時間用 t 參數重新載入，並附上「在 Bilibili 開啟這個時間」。
//   - 本機檔案、Podcast：<video>／<audio> 指向 GET /api/items/{id}/media（後端支援 Range）。
//
// 收到的訊息一律驗證來源（origin 在允許清單內、source 是這個 iframe），JSON 解析失敗就丟掉。
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type Ref,
} from "react";
import { useTranslation } from "react-i18next";

import { apiPath } from "../../api/client";
import { formatTimecode } from "../../i18n";

export interface PlayerHandle {
  seek: (seconds: number) => void;
}

export interface PlayerProps {
  itemId: string;
  platform: string;
  externalId: string;
  part?: number;
  title: string;
  /** 起始時間（秒，網址的 t） */
  start?: number;
  onTime?: (seconds: number) => void;
}

export const YT_ORIGINS = ["https://www.youtube-nocookie.com", "https://www.youtube.com"] as const;
const YT_EMBED = YT_ORIGINS[0];
const FALLBACK_MS = 4000;
const HANDSHAKE_MS = 250;

export function youtubeEmbedUrl(videoId: string, start: number, autoplay: boolean, origin: string) {
  const q = new URLSearchParams({
    enablejsapi: "1",
    origin,
    playsinline: "1",
    rel: "0",
    start: String(Math.max(0, Math.floor(start))),
  });
  if (autoplay) q.set("autoplay", "1");
  return `${YT_EMBED}/embed/${encodeURIComponent(videoId)}?${q}`;
}

/** Bilibili 的 external_id 可能帶 _p<n>（yt-dlp 的分P id） */
export function bilibiliIds(externalId: string, part?: number) {
  const m = /^(BV[0-9A-Za-z]+)(?:_p(\d+))?$/.exec(externalId);
  const bvid = m?.[1] ?? externalId;
  const p = part ?? (m?.[2] ? Number(m[2]) : 1);
  return { bvid, p: Math.max(1, p) };
}

export function bilibiliEmbedUrl(
  externalId: string,
  part: number | undefined,
  start: number,
  autoplay: boolean,
) {
  const { bvid, p } = bilibiliIds(externalId, part);
  const q = new URLSearchParams({
    bvid,
    p: String(p),
    t: String(Math.max(0, Math.floor(start))),
    autoplay: autoplay ? "1" : "0",
    high_quality: "1",
  });
  return `https://player.bilibili.com/player.html?${q}`;
}

export function bilibiliPageUrl(externalId: string, part: number | undefined, start: number) {
  const { bvid, p } = bilibiliIds(externalId, part);
  return `https://www.bilibili.com/video/${encodeURIComponent(bvid)}?p=${p}&t=${Math.max(0, Math.floor(start))}`;
}

/** YouTube 播放器的訊息：只取 currentTime；格式不對回傳 undefined */
export function parseYouTubeMessage(data: unknown): { ready?: true; time?: number } | undefined {
  let v: unknown = data;
  if (typeof data === "string") {
    try {
      v = JSON.parse(data);
    } catch {
      return undefined;
    }
  }
  if (!v || typeof v !== "object") return undefined;
  const ev = (v as { event?: unknown }).event;
  if (ev === "onReady" || ev === "initialDelivery") return { ready: true };
  if (ev === "infoDelivery") {
    const t = (v as { info?: { currentTime?: unknown } }).info?.currentTime;
    return typeof t === "number" && Number.isFinite(t) ? { ready: true, time: t } : { ready: true };
  }
  return undefined;
}

function YouTubePlayer(
  { externalId, title, start = 0, onTime }: PlayerProps,
  ref: Ref<PlayerHandle>,
) {
  const { t } = useTranslation();
  const frame = useRef<HTMLIFrameElement>(null);
  const [src, setSrc] = useState(() =>
    youtubeEmbedUrl(externalId, start, false, window.location.origin),
  );
  // 播放器有沒有回應過 postMessage；沒有回應就用重新載入的方式跳轉
  const live = useRef(false);
  const [fallback, setFallback] = useState(false);
  const onTimeRef = useRef(onTime);
  useEffect(() => {
    onTimeRef.current = onTime;
  });

  const post = useCallback((msg: object) => {
    // 只送到實際嵌入的 origin（nocookie），送到其他 origin 會在 console 產生 target origin 不符的錯誤
    frame.current?.contentWindow?.postMessage(JSON.stringify(msg), YT_EMBED);
  }, []);

  useEffect(() => {
    function onMessage(e: MessageEvent) {
      if (!(YT_ORIGINS as readonly string[]).includes(e.origin)) return;
      if (e.source !== frame.current?.contentWindow) return;
      const m = parseYouTubeMessage(e.data);
      if (!m) return;
      live.current = true;
      setFallback(false);
      if (m.time !== undefined) onTimeRef.current?.(m.time);
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  const timer = useRef<number | undefined>(undefined);
  const handshake = useRef<number | undefined>(undefined);
  useEffect(
    () => () => {
      window.clearTimeout(timer.current);
      window.clearInterval(handshake.current);
    },
    [],
  );
  const onLoad = useCallback(() => {
    // 「listening」讓播放器開始送 infoDelivery（含 currentTime）。iframe 的 load 可能早於播放器初始化，
    // 所以每 250ms 重送，直到收到第一則訊息或逾時（之後降級成重新載入）
    const hello = () => post({ event: "listening", id: 1, channel: "widget" });
    hello();
    window.clearInterval(handshake.current);
    handshake.current = window.setInterval(() => {
      if (live.current) window.clearInterval(handshake.current);
      else hello();
    }, HANDSHAKE_MS);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      window.clearInterval(handshake.current);
      if (!live.current) setFallback(true);
    }, FALLBACK_MS);
  }, [post]);

  useImperativeHandle(ref, () => ({
    seek: (s) => {
      if (live.current) {
        post({ event: "command", func: "seekTo", args: [s, true], id: 1, channel: "widget" });
        post({ event: "command", func: "playVideo", args: [], id: 1, channel: "widget" });
        onTimeRef.current?.(s);
      } else {
        setSrc(youtubeEmbedUrl(externalId, s, true, window.location.origin));
        onTimeRef.current?.(s);
      }
    },
  }));

  return (
    <div className="player player--video">
      <iframe
        ref={frame}
        className="player__frame"
        src={src}
        title={t("item.player.frameTitle", { title })}
        allow="autoplay; encrypted-media; picture-in-picture; fullscreen"
        // 頁面的 Referrer-Policy 是 same-origin；YouTube 嵌入需要知道來源網域，只在這個元素放寬
        referrerPolicy="strict-origin-when-cross-origin"
        allowFullScreen
        onLoad={onLoad}
      />
      {fallback && <p className="player__note secondary">{t("item.player.reloadFallback")}</p>}
    </div>
  );
}

function BilibiliPlayer(
  { externalId, part, title, start = 0, onTime }: PlayerProps,
  ref: Ref<PlayerHandle>,
) {
  const { t } = useTranslation();
  const [at, setAt] = useState({ s: start, autoplay: false });
  useImperativeHandle(ref, () => ({
    seek: (s) => {
      setAt({ s, autoplay: true });
      onTime?.(s);
    },
  }));
  return (
    <div className="player player--video">
      <iframe
        className="player__frame"
        src={bilibiliEmbedUrl(externalId, part, at.s, at.autoplay)}
        title={t("item.player.frameTitle", { title })}
        allow="autoplay; encrypted-media; picture-in-picture; fullscreen"
        referrerPolicy="strict-origin-when-cross-origin"
        allowFullScreen
      />
      <p className="player__note secondary">
        <a href={bilibiliPageUrl(externalId, part, at.s)} target="_blank" rel="noreferrer noopener">
          {t("item.player.openBilibiliAt", { time: formatTimecode(at.s) })}
        </a>
      </p>
    </div>
  );
}

function MediaPlayer(
  { itemId, platform, title, start = 0, onTime }: PlayerProps,
  ref: Ref<PlayerHandle>,
) {
  const { t } = useTranslation();
  const el = useRef<HTMLMediaElement | null>(null);
  const [failed, setFailed] = useState(false);
  const src = apiPath("/api/items/{id}/media", { id: itemId });
  useImperativeHandle(ref, () => ({
    seek: (s) => {
      const m = el.current;
      if (!m || failed) return;
      try {
        m.currentTime = s;
        void m.play()?.catch(() => {});
      } catch {
        // jsdom 與還沒載入 metadata 的元素可能不接受
      }
      onTime?.(s);
    },
  }));
  const common = {
    src: start ? `${src}#t=${Math.floor(start)}` : src,
    controls: true,
    preload: "metadata" as const,
    onTimeUpdate: () => onTime?.(el.current?.currentTime ?? 0),
    onError: () => setFailed(true),
    "aria-label": t("item.player.mediaLabel", { title }),
  };
  if (failed) {
    return (
      <div className="player player--missing">
        <p className="secondary">{t("item.player.mediaUnavailable")}</p>
      </div>
    );
  }
  return platform === "podcast" ? (
    <div className="player player--audio">
      <audio ref={(n) => void (el.current = n)} {...common} />
    </div>
  ) : (
    <div className="player player--video">
      <video ref={(n) => void (el.current = n)} className="player__frame" {...common} />
    </div>
  );
}

const YouTube = forwardRef(YouTubePlayer);
const Bilibili = forwardRef(BilibiliPlayer);
const Media = forwardRef(MediaPlayer);

export const Player = forwardRef<PlayerHandle, PlayerProps>(function Player(props, ref) {
  switch (props.platform) {
    case "youtube":
      return <YouTube ref={ref} {...props} />;
    case "bilibili":
      return <Bilibili ref={ref} {...props} />;
    default:
      return <Media ref={ref} {...props} />;
  }
});
