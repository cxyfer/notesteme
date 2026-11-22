// 步驟的 actions（API 回傳的是後端的繁中按鈕文字，不是代碼）→ 畫面上的動作。
// 對照表的鍵是後端的字串（internal/**：Action* 與 action* 常數）；認不得的字串原樣顯示，並連到設定頁。
// 缺口：API 應改回傳動作代碼（見 docs/ui/README.md 10.7）。
import type { SettingsSection } from "../SettingsPage";

export type ActionKind =
  | { kind: "rerun" }
  | { kind: "switchToApi" }
  | { kind: "uploadCookie" }
  | { kind: "updateYtDlp" }
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
  現在改用語音轉錄: { key: "useAsrNow", action: { kind: "settings", section: "services" } },
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
};

export function mapActions(raw: readonly string[] | null | undefined): StageAction[] {
  const seen = new Set<string>();
  const out: StageAction[] = [];
  for (const r of raw ?? []) {
    const label = r.trim();
    if (!label || seen.has(label)) continue;
    seen.add(label);
    const hit = TABLE[label];
    out.push(hit ? { raw: label, ...hit } : { raw: label, action: { kind: "unknown" } });
  }
  return out;
}
