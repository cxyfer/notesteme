// e2eseed 在 genlib 產生的資料目錄上補寫瀏覽器 e2e（web/client/e2e）需要、benchdata 本身不產生的資料：
// 繁中筆記與摘要（閱讀字型、筆記分頁）、章節與關鍵畫面（影音詳情的分頁）、一個訂閱與它的新影片（訂閱頁）、
// 幾筆成本（成本頁的金額格式）。
//
//	go run ./internal/benchdata/cmd/genlib -out <dir> -profile lib-10k -scale 0.01
//	go run ./internal/benchdata/cmd/e2eseed -data <dir>
//
// 筆記、摘要、章節與畫面經過驗收 harness 使用的 app.Server 的 Seed* 方法（ADR-0015），與產品寫入的產出格式相同；
// 訂閱與成本經過 store 的公開方法。程序內的 server 以 --insecure-no-auth 啟動、worker 不執行、不連任何網路，
// 寫完就關閉：之後由真正的 notesteme binary 以 NOTESTEME_PASSWORD 啟動 serve（密碼在那時才寫入，雜湊參數與產品相同）。
//
// 執行結果（被補寫的影音 ID）以 JSON 印到 stdout，e2e 的 global setup 依它找頁面。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// seeded 是印到 stdout 的結果。
type seeded struct {
	// NoteItem 是有繁中筆記、摘要、章節與畫面的影音（原文 zh-Hant）。
	NoteItem string `json:"note_item"`
	// EnItem 是原文英文、有章節的影音。
	EnItem string `json:"en_item"`
	// Subscription 是訂閱的 ID。
	Subscription string `json:"subscription"`
}

func main() {
	data := flag.String("data", "", "genlib 產生的資料目錄（必填）")
	flag.Parse()
	if *data == "" {
		fmt.Fprintln(os.Stderr, "e2eseed: 需要 -data")
		os.Exit(2)
	}
	out, err := run(context.Background(), *data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2eseed: %v\n", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func run(ctx context.Context, dir string) (seeded, error) {
	var out seeded
	st, err := store.Open(ctx, dir, clock.Real{})
	if err != nil {
		return out, err
	}
	items, err := pickItems(ctx, st)
	if err != nil {
		_ = st.Close()
		return out, err
	}
	out.NoteItem, out.EnItem = items.zh.ID, items.en.ID
	if err := st.Tx(ctx, func(r *store.Repo) error {
		sub, err := seedSubscription(ctx, r, items)
		out.Subscription = sub
		if err != nil {
			return err
		}
		return seedCosts(ctx, r, items)
	}); err != nil {
		_ = st.Close()
		return out, err
	}
	if err := st.Close(); err != nil {
		return out, err
	}

	srv, err := app.StartServerWith(ctx, app.Env{
		Vars: map[string]string{
			"NOTESTEME_DATA":   dir,
			"NOTESTEME_CONFIG": os.DevNull,
			// 只有 PATH 上沒有的工具：worker 不執行，Seed* 也不呼叫外部工具。
			"PATH":                 "/nonexistent",
			"NOTESTEME_MASTER_KEY": os.Getenv("NOTESTEME_MASTER_KEY"),
		},
		Clock: clock.Real{},
		Net:   netguard.SystemDeps(),
		// HTTP 改聽 127.0.0.1 的任意埠（不和之後的 serve 搶埠）；本機 socket 照原路徑。
		Listen: func(network, addr string) (net.Listener, error) {
			if network == "tcp" {
				addr = "127.0.0.1:0"
			}
			return net.Listen(network, addr)
		},
		Log:  io.Discard,
		Test: &app.TestControls{ManualWorker: true, SkipShutdownGrace: true, NoYtDlpAutoUpdate: true},
	}, app.ServeOptions{InsecureNoAuth: true})
	if err != nil {
		return out, fmt.Errorf("啟動程序內的 server：%w", err)
	}
	seedErr := seedOutputs(ctx, srv, items)
	sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && seedErr == nil {
		seedErr = err
	}
	return out, seedErr
}

type pickedItems struct{ zh, en store.Item }

// pickItems 取最早建立的一部 zh-Hant 與一部 en 影音（benchdata 的第 1、0 部；第 5 部的 segment 是 failed，避開）。
func pickItems(ctx context.Context, st *store.Store) (pickedItems, error) {
	var p pickedItems
	list, err := st.Read().ListItems(ctx) // 依新增順序
	if err != nil {
		return p, err
	}
	for _, it := range list {
		switch {
		case p.zh.ID == "" && it.OriginalLang == "zh-Hant":
			p.zh = it
		case p.en.ID == "" && it.OriginalLang == "en":
			p.en = it
		}
	}
	if p.zh.ID == "" || p.en.ID == "" {
		return p, fmt.Errorf("資料目錄裡找不到 zh-Hant 與 en 的影音（先執行 genlib）")
	}
	return p, nil
}

// seedSubscription 建立一個 YouTube 頻道的訂閱，兩部影音是它建立的新影片、一部被篩選略過。
// 停用（Enabled=false）：serve 啟動後排程器不會去檢查它（假的 yt-dlp 不會辨識頻道，檢查只會留下錯誤）。
func seedSubscription(ctx context.Context, r *store.Repo, p pickedItems) (string, error) {
	now := time.Now().UTC()
	sub, err := r.InsertSubscription(ctx, store.Subscription{
		Kind: "channel", Platform: "youtube", ExternalID: "UCe2eseedxxxxxxxxxxxxxxx",
		URL: "https://www.youtube.com/channel/UCe2eseedxxxxxxxxxxxxxxx", Title: "E2E 頻道",
		Schedule: 6 * time.Hour, Enabled: false,
	})
	if err != nil {
		return "", err
	}
	for i, e := range []struct {
		it       store.Item
		decision string
		reason   string
	}{{p.en, store.EntryCreated, ""}, {p.zh, store.EntryCreated, ""}, {store.Item{Platform: "youtube", ExternalID: "e2eskipped1", Title: "Shorts"}, store.EntrySkipped, "shorts"}} {
		pub := now.Add(-time.Duration(i+1) * 24 * time.Hour)
		dur := 61 + i*600
		if err := r.UpsertSubscriptionEntry(ctx, store.SubscriptionEntry{
			SubscriptionID: sub.ID, Platform: "youtube", ExternalID: e.it.ExternalID, Title: e.it.Title,
			PublishedAt: &pub, DurationS: &dur, LiveStatus: "not_live", Position: i, Decision: e.decision, Reason: e.reason,
			ItemID: e.it.ID, SeenAt: now,
		}); err != nil {
			return "", err
		}
	}
	return sub.ID, nil
}

// seedCosts 寫入兩部影音的 LLM 花費（成本頁、總覽與詳情的金額）。
func seedCosts(ctx context.Context, r *store.Repo, p pickedItems) error {
	for _, c := range []store.CostRow{
		{ItemID: p.en.ID, ItemTitle: p.en.Title, Stage: pipeline.StageTranslate, Provider: "openai", Model: "gpt-4.1-mini",
			Units: 0.0123, UnitType: "input_mtok", USD: 1234.5678, State: store.CostActual, Attempt: 1, Outcome: "ok"},
		{ItemID: p.zh.ID, ItemTitle: p.zh.Title, Stage: pipeline.StageNote, Provider: "openai", Model: "gpt-4.1-mini",
			Units: 0.002, UnitType: "output_mtok", USD: 0.0042, State: store.CostActual, Attempt: 1, Outcome: "ok"},
	} {
		c.At = time.Now().UTC()
		if _, err := r.InsertCostRow(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// zhNote 是繁中筆記：閱讀字型只該載入 Noto Serif TC 的切片與拉丁字（G1，NFR-I18N-3）。
const zhNote = `---
title: 影片筆記
lang: zh-Hant
---
# 影片筆記

## 重點 [00:12]

這部影片介紹資料庫的索引、交易隔離與查詢計畫。繁體中文的段落用來檢查閱讀字型：
臺灣、資料、檢查、說明、範例與測試。English words such as PostgreSQL and B-tree stay in Latin.

- 索引讓查詢不必掃描整張表。
- 交易隔離層級決定讀到哪些變更。

## 自我檢查

1. 什麼時候該建立複合索引？
`

func seedOutputs(ctx context.Context, srv *app.Server, p pickedItems) error {
	if err := srv.SeedNote(ctx, p.zh.ID, zhNote); err != nil {
		return fmt.Errorf("筆記：%w", err)
	}
	summary, _ := json.Marshal(map[string]any{
		"summary":           "這部影片說明資料庫索引與交易隔離。",
		"key_points":        []string{"索引減少掃描", "隔離層級決定可見的變更"},
		"title_translation": "影片 1：資料庫索引與交易",
		"language":          "zh-Hant",
	})
	if err := srv.SeedSummary(ctx, p.zh.ID, summary); err != nil {
		return fmt.Errorf("摘要：%w", err)
	}
	for _, it := range []store.Item{p.zh, p.en} {
		if err := srv.SeedChapters(ctx, it.ID, []pipeline.SeedChapter{
			{Title: "開場", StartMS: 0, Cue: 1}, {Title: "索引", StartMS: 60_000, Cue: 20}, {Title: "交易", StartMS: 180_000, Cue: 60},
		}); err != nil {
			return fmt.Errorf("章節：%w", err)
		}
	}
	if err := srv.SeedFrames(ctx, p.zh.ID, []pipeline.SeedFrame{
		{AtMS: 12_000, Selected: true, Caption: "架構圖", Chapter: "開場", Color: "#336699"},
		{AtMS: 75_000, Selected: false, Caption: "索引結構", Chapter: "索引", Color: "#996633"},
	}); err != nil {
		return fmt.Errorf("畫面：%w", err)
	}
	return nil
}
