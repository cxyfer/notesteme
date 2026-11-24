// 步驟的 actions（API 回傳的是後端的繁中按鈕文字）→ 畫面上的動作。
// 對照表的鍵是後端的字串（internal/**：Action* 與 action* 常數）；認不得的字串原樣顯示，並連到設定頁。
// 有穩定代碼的動作（Stage.action_codes，目前只有 use_asr_now）先依代碼對應，同一個動作的文字不再重複顯示；
// 其他動作仍靠文字對照（見 docs/ui/README.md 10.7）。
import type { SettingsSection } from "../SettingsPage";

export type ActionKind =
  // from：從哪個步驟重跑；省略時是顯示這個動作的步驟
  | { kind: "rerun"; from?: string }
  // UI-5（DIG-R6、R7）：摘要後暫停的「繼續處理」與「取消」
  | { kind: "continue" }
  | { kind: "cancel" }
  | { kind: "switchToApi" }
  | { kind: "uploadCookie" }
  | { kind: "updateYtDlp" }
  // SRC-R6：略過等待自動字幕，立刻改用語音轉錄（POST /api/items/{id}/use-asr-now）
  | { kind: "useAsrNow" }
  | { kind: "settings"; section: SettingsSection }
  | { kind: "unknown" };

export interface StageAction {
  /** i18n key（actions.*）；unknown 時是 undefined，顯示後端的原文 */
  key?: string;
  raw: string;
  action: ActionKind;
}

const TABLE: Record<string, { key: string; action: ActionKind }> = {
  重試: { key: "retry", action: { kind: "rerun" } },
  重新翻譯: { key: "retranslate", action: { kind: "rerun" } },
  重新校正這段: { key: "recorrectChunk", action: { kind: "rerun" } },
  處理該檔案後重新發布: { key: "republish", action: { kind: "rerun" } },
  現在改用語音轉錄: { key: "useAsrNow", action: { kind: "useAsrNow" } },
  改用語音轉錄: { key: "useAsr", action: { kind: "settings", section: "services" } },
  "改用 API 處理": { key: "switchToApi", action: { kind: "switchToApi" } },
  "上傳 cookie": { key: "uploadCookie", action: { kind: "uploadCookie" } },
  "更新 yt-dlp": { key: "updateYtDlp", action: { kind: "updateYtDlp" } },
  "安裝 yt-dlp": { key: "installYtDlp", action: { kind: "settings", section: "system" } },
  "安裝 ffmpeg": { key: "installFfmpeg", action: { kind: "settings", section: "system" } },
  "安裝 ffprobe": { key: "installFfprobe", action: { kind: "settings", section: "system" } },
  "設定 API key": { key: "setApiKey", action: { kind: "settings", section: "services" } },
  設定語音轉錄服務: { key: "setupAsr", action: { kind: "settings", section: "services" } },
  "升級 Supadata 方案": {
    key: "upgradeSupadata",
    action: { kind: "settings", section: "services" },
  },
  "檢查 provider 額度": { key: "checkQuota", action: { kind: "settings", section: "services" } },
  更換模型: { key: "changeModel", action: { kind: "rerun" } },
  // Prompt 庫是 UI-3：先打開重新處理對話框，在那裡選 prompt 版本
  "開啟 Prompt": { key: "openPrompt", action: { kind: "rerun" } },
  // UI-5：DIG（internal/pipeline/summary.go）、SRC-10 與 SPB（reprobe.go、sponsorblock.go）
  繼續處理: { key: "continue", action: { kind: "continue" } },
  取消: { key: "cancelItem", action: { kind: "cancel" } },
  重新產生摘要: { key: "regenerateSummary", action: { kind: "rerun" } },
  改用更好的字幕重跑: { key: "rerunBetter", action: { kind: "rerun", from: "transcript" } },
  "重新查詢 SponsorBlock": { key: "requerySponsor", action: { kind: "rerun" } },
  從章節重跑: { key: "rerunChapters", action: { kind: "rerun", from: "chapters" } },
};

// 動作代碼（後端 pipeline.ActionCode*）→ 動作；label 是同一個動作在 actions 中的文字。
const CODES: Record<string, { key: string; label: string; action: ActionKind }> = {
  use_asr_now: { key: "useAsrNow", label: "現在改用語音轉錄", action: { kind: "useAsrNow" } },
};

export function mapActions(
  raw: readonly string[] | null | undefined,
  codes?: readonly string[] | null,
): StageAction[] {
  const seen = new Set<string>();
  const out: StageAction[] = [];
  for (const c of codes ?? []) {
    const hit = CODES[c];
    if (!hit || seen.has(hit.label)) continue;
    seen.add(hit.label);
    out.push({ raw: hit.label, key: hit.key, action: hit.action });
  }
  for (const r of raw ?? []) {
    const label = r.trim();
    if (!label || seen.has(label)) continue;
    seen.add(label);
    const hit = TABLE[label];
    out.push(hit ? { raw: label, ...hit } : { raw: label, action: { kind: "unknown" } });
  }
  return out;
}
