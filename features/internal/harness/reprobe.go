package harness

import "github.com/cxyfer/notesteme/features/internal/faketool"

// SyncNow 把 fake clock 現在的時間寫給假工具（SRC-10：事後補上的字幕軌依它決定是否出現）。
func (w *World) SyncNow() error { return faketool.WriteNow(w.Root, w.Clock.Now()) }
