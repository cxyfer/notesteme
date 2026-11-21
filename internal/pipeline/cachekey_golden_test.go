package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// YouTube（與 Bilibili）的 transcript 快取鍵必須不因為 Track 新增欄位而改變：
// 否則升級後所有既有影音的快取都會失效，重跑時重複付費（PIPE-R5）。
func TestYouTubeTranscriptCacheKeyGolden(t *testing.T) {
	tracks := []transcript.Track{{Lang: "zh-Hant", Kind: "auto"}, {Lang: "en", Kind: "manual"}}
	b, err := json.Marshal(sortedTracks(tracks))
	if err != nil {
		t.Fatal(err)
	}
	const wantTracks = `[{"Lang":"en","Kind":"manual"},{"Lang":"zh-Hant","Kind":"auto"}]`
	if string(b) != wantTracks {
		t.Fatalf("tracks 的 JSON = %s，預期 %s", b, wantTracks)
	}

	upstream := paramsSHA(map[string]any{"tracks": sortedTracks(tracks), "original_lang": "en"})
	params := paramsSHA(map[string]any{
		"caption_priority":          []transcript.Rule{{Lang: "original", Kind: "manual"}},
		"allow_platform_translated": false,
		"fallback":                  []string{"supadata", "groq"},
	})
	key := cacheKey{Item: "item-1", Stage: StageTranscript, Impl: 1, Provider: "yt-dlp", Upstream: upstream, Params: params}.String()
	// 這些值在 Track 新增欄位之前算出；改動後仍必須相同。
	const (
		wantUpstream = "946a4b6b818a1280f8661a7dd6a1f742dfc8f95f1436eb94255a4567f968f5eb"
		wantParams   = "a3e518aabd3c2f5ef55b8faf2a842640114e8022de4ea85da45861d81edf83e0"
		wantKey      = "0735d8270d6e4a05282e6160992a114e50186e530a86967471a6271733c9fff0"
	)
	if upstream != wantUpstream || params != wantParams || key != wantKey {
		t.Errorf("快取鍵改變了：upstream = %s，params = %s，key = %s", upstream, params, key)
	}
}

// 會用到 fallback 來源（沒選到字幕軌）時，快取鍵含 Groq 的參數與前處理設定（preprocess）。
// M1 尚未發布，前處理參數進快取鍵是刻意的改變；這個 golden 把目前的鍵固定下來，
// 之後改動 Groq 或前處理參數造成鍵改變時必須明確更新。
func TestYouTubeTranscriptCacheKeyWithFallbackGolden(t *testing.T) {
	r, g := newGroqRig(t)
	g.noKey = true // 不轉錄：只取結果的快取鍵
	r.yt.info.Tracks = nil
	it := store.Item{ID: "item-1", Platform: "youtube", ExternalID: "abc", OriginalLang: "en", LangSource: "metadata"}

	res, err := r.p.runTranscript(context.Background(), it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if res.stage == nil {
		t.Fatalf("result = %+v", res)
	}
	const wantKey = "dcefcc7b44621247e5812c1bbbd38d81627173f920f8c698bd8b9fea81f44a64"
	if res.stage.CacheKey != wantKey {
		t.Errorf("含 fallback 的快取鍵改變了：%s", res.stage.CacheKey)
	}
}
