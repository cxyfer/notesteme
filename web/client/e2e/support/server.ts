// 啟動真正的 notesteme binary（serve，內嵌 web/client/dist）給 e2e 使用：
//   1. genlib 產生資料目錄（lib-10k 的縮小版，或 10,000 列的 perf 版）；
//   2. e2eseed 補上筆記、摘要、章節、畫面、訂閱與成本；
//   3. 假的 yt-dlp／ffmpeg（shell script，不連網路；「新增」抽屜的辨識用）與不連外的設定檔；
//   4. 以固定的測試密碼啟動 serve，等 /readyz。
// 外部網路一律不碰（NFR-TEST-1）：SponsorBlock 指向不存在的 loopback 埠、yt-dlp 是替身、yt-dlp 不自動更新。
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { existsSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const clientDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
export const repoRoot = path.resolve(clientDir, "../..");
export const outDir = path.join(clientDir, "e2e", ".out");

export const PASSWORD = "e2e-password-not-secret";
const MASTER_KEY = "e2e-master-key-not-secret";

export interface SeedResult {
  note_item: string;
  en_item: string;
  subscription: string;
}

export interface Served {
  base: string;
  root: string;
  proc: ChildProcess;
  seed: SeedResult | null;
  stop: () => Promise<void>;
}

export function binaryPath(): string {
  const bin = process.env.NOTESTEME_E2E_BIN ?? path.join(outDir, "notesteme");
  if (!existsSync(bin)) {
    throw new Error(
      `找不到 notesteme binary：${bin}。先 npm run build，再在 repo 根目錄 go build -o ${bin} ./cmd/notesteme（或設定 NOTESTEME_E2E_BIN）。`,
    );
  }
  return bin;
}

/** 確認 binary 內嵌的是真正的 UI，不是 internal/webui 的 Placeholder（沒有 npm run build 就 go build 的情況）。 */
async function assertRealUI(base: string) {
  const html = await (await fetch(`${base}/`)).text();
  if (!/\/assets\/[^"]+\.js/.test(html)) {
    throw new Error(
      "serve 回傳的不是建置好的 Client UI（沒有 /assets/*.js）：先 npm run build 再 go build。",
    );
  }
}

function goRun(args: string[], env: NodeJS.ProcessEnv = {}): string {
  const r = spawnSync("go", ["run", ...args], {
    cwd: repoRoot,
    env: { ...process.env, ...env },
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
  if (r.status !== 0) {
    throw new Error(`go run ${args.join(" ")} 失敗（${r.status}）：\n${r.stderr}`);
  }
  return r.stdout;
}

export interface DataOptions {
  /** genlib 的 -scale（lib-10k 的比例） */
  scale: number;
  /** genlib 的 -cues（0 用 profile 預設） */
  cues?: number;
  /** 是否執行 e2eseed */
  seed: boolean;
}

/** 產生資料目錄（已存在且有 .ready 標記時沿用）。 */
export function generateData(dir: string, o: DataOptions): SeedResult | null {
  const marker = path.join(dir, ".e2e-ready.json");
  if (existsSync(marker)) return JSON.parse(readFileSync(marker, "utf8")) as SeedResult | null;
  rmSync(dir, { recursive: true, force: true });
  const args = ["./internal/benchdata/cmd/genlib", "-out", dir, "-profile", "lib-10k"];
  args.push("-scale", String(o.scale));
  if (o.cues) args.push("-cues", String(o.cues));
  goRun(args);
  let seed: SeedResult | null = null;
  if (o.seed) {
    seed = JSON.parse(
      goRun(["./internal/benchdata/cmd/e2eseed", "-data", dir], {
        NOTESTEME_MASTER_KEY: MASTER_KEY,
      }),
    ) as SeedResult;
  }
  writeFileSync(marker, JSON.stringify(seed));
  return seed;
}

/** yt-dlp 的替身：--version 回版本，-J 回一部有英文字幕的公開影片（id 取自網址），其餘失敗。 */
function writeFakeTools(root: string) {
  const bin = path.join(root, "bin");
  mkdirSync(bin, { recursive: true });
  const info =
    '{"id":"__ID__","title":"E2E 新影片 __ID__","duration":754,"language":"en","availability":"public",' +
    '"live_status":"not_live","channel_id":"UCe2eaddxxxxxxxxxxxxxxxx","channel":"E2E Channel","upload_date":"20261101",' +
    '"subtitles":{"en":[{"ext":"vtt","url":"https://example.invalid/en.vtt"}]},"automatic_captions":{}}';
  const script = `#!/bin/sh
for a in "$@"; do
  case "$a" in
    --version) echo 2026.11.01; exit 0 ;;
    -J) J=1 ;;
  esac
done
if [ -n "$J" ]; then
  id=$(printf '%s' "$*" | /usr/bin/sed -n 's/.*v=\\([A-Za-z0-9_-]\\{11\\}\\).*/\\1/p')
  printf '%s\\n' '${info}' | /usr/bin/sed "s/__ID__/$id/g"
  exit 0
fi
echo "e2e: yt-dlp 替身不支援：$*" >&2
exit 2
`;
  writeFileSync(path.join(bin, "yt-dlp"), script, { mode: 0o755 });
  writeFileSync(path.join(bin, "ffmpeg"), "#!/bin/sh\necho 'ffmpeg version 7.0'\n", {
    mode: 0o755,
  });
  // SponsorBlock 指向不存在的 loopback 埠（SPB-R5 存成空的產出），不連外。
  writeFileSync(path.join(root, "config.yaml"), "sponsorblock:\n  base_url: http://127.0.0.1:9\n", {
    mode: 0o600,
  });
}

export async function startServe(opts: {
  port: number;
  data: DataOptions;
  /** 資料目錄（沿用時省去產生的時間）；預設在暫存目錄 */
  dataDir?: string;
}): Promise<Served> {
  const bin = binaryPath();
  const root = await mkdtemp(path.join(tmpdir(), "notesteme-e2e-"));
  const dataDir = opts.dataDir ?? path.join(root, "data");
  const seed = generateData(dataDir, opts.data);
  writeFakeTools(root);
  const listen = `127.0.0.1:${opts.port}`;
  const log = openSync(path.join(root, "serve.log"), "a");
  const proc = spawn(bin, ["serve"], {
    cwd: root,
    env: {
      PATH: `${path.join(root, "bin")}:/usr/bin:/bin`,
      HOME: root,
      NOTESTEME_DATA: dataDir,
      NOTESTEME_CONFIG: path.join(root, "config.yaml"),
      NOTESTEME_MASTER_KEY: MASTER_KEY,
      NOTESTEME_PASSWORD: PASSWORD,
      NOTESTEME_LISTEN: listen,
      TZ: "Asia/Taipei",
    },
    stdio: ["ignore", log, log],
  });
  const base = `http://${listen}`;
  const deadline = Date.now() + 60_000;
  for (;;) {
    if (proc.exitCode !== null) {
      throw new Error(
        `serve 結束（${proc.exitCode}）：\n${readFileSync(path.join(root, "serve.log"), "utf8").slice(-4000)}`,
      );
    }
    try {
      const r = await fetch(`${base}/readyz`);
      if (r.ok) break;
    } catch {
      // 還沒開始 listen
    }
    if (Date.now() > deadline) throw new Error(`serve 在 60 秒內沒有就緒（${base}）`);
    await new Promise((r) => setTimeout(r, 200));
  }
  await assertRealUI(base);
  const stop = async () => {
    if (proc.exitCode !== null) return;
    proc.kill("SIGTERM");
    await new Promise<void>((resolve) => {
      const t = setTimeout(() => {
        proc.kill("SIGKILL");
        resolve();
      }, 30_000);
      proc.once("exit", () => {
        clearTimeout(t);
        resolve();
      });
    });
  };
  return { base, root, proc, seed, stop };
}

/** 以測試密碼登入，回傳 Playwright 的 storageState（cookie 不分引擎）。 */
export async function login(base: string) {
  const r = await fetch(`${base}/api/auth/login`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ password: PASSWORD }),
  });
  if (!r.ok) throw new Error(`登入失敗：${r.status} ${await r.text()}`);
  const host = new URL(base).hostname;
  const cookies = r.headers.getSetCookie().map((c) => {
    const [pair, ...attrs] = c.split(";").map((s) => s.trim());
    const eq = pair!.indexOf("=");
    const lower = attrs.map((a) => a.toLowerCase());
    return {
      name: pair!.slice(0, eq),
      value: pair!.slice(eq + 1),
      domain: host,
      path: "/",
      expires: -1,
      httpOnly: lower.includes("httponly"),
      secure: lower.includes("secure"),
      sameSite: "Lax" as const,
    };
  });
  return { cookies, origins: [] as never[] };
}

/** 帶 session 與 CSRF 呼叫 API（global setup 準備資料用）。 */
export async function apiCall(
  base: string,
  state: Awaited<ReturnType<typeof login>>,
  method: string,
  url: string,
  body?: unknown,
) {
  const cookie = state.cookies.map((c) => `${c.name}=${c.value}`).join("; ");
  const csrf = state.cookies.find((c) => c.name === "notesteme_csrf")?.value ?? "";
  const r = await fetch(`${base}${url}`, {
    method,
    headers: {
      cookie,
      "x-csrf-token": csrf,
      origin: base,
      ...(body === undefined ? {} : { "content-type": "application/json" }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await r.text();
  if (!r.ok) throw new Error(`${method} ${url}：${r.status} ${text}`);
  return text ? (JSON.parse(text) as unknown) : null;
}
