// 影音詳情的播放快捷鍵（docs/ui/README.md §3.6，UI-8）：Space、←／→、[／]。

/** 離目前章節開頭超過這麼多秒時，[ 先回到目前章節的開頭（和多數播放器相同） */
const RESTART_WITHIN = 2;

/**
 * [（dir = -1）與 ]（dir = 1）要跳到的時間；沒有上一個或下一個章節時回傳 undefined。
 * starts 是章節開始的秒數（依時間排序）。
 */
export function chapterTarget(starts: readonly number[], now: number, dir: -1 | 1) {
  if (dir === 1) return starts.find((s) => s > now + 0.5);
  let i = -1;
  for (let k = 0; k < starts.length; k++) if (starts[k]! <= now + 0.001) i = k;
  if (i < 0) return undefined;
  if (now - starts[i]! > RESTART_WITHIN) return starts[i];
  return i > 0 ? starts[i - 1] : undefined;
}

/**
 * Space 與方向鍵在這些元素上有自己的作用（按鈕、連結、分頁、時間帶的 slider、選項、媒體元素…），不接手。
 * main 本身（換頁後焦點所在）與一般文字不算。
 */
const OWN_KEYS =
  "a[href],button,input,select,textarea,summary,video,audio,iframe,[contenteditable],[role='tab'],[role='slider'],[role='option'],[role='button'],[role='checkbox'],[role='switch'],[role='menuitem'],[role='listbox'],[role='grid']";

export function ownsPlaybackKeys(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest(OWN_KEYS) !== null;
}
