package fakellm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateByTarget(t *testing.T) {
	args := json.RawMessage(`{"texts": {"zh-Hant-TW": "譯文 {i}", "ja": "訳 {i}"}}`)
	in := GeneratorInput{HasData: true, Chunk: 1, Chunks: 1, Prefix: "…目標語言：ja。…",
		Cues: []map[string]any{{"i": json.Number("3"), "text": "a" + splitMarker + "b"}, {"i": json.Number("5"), "text": "c"}}}
	got, err := runByTarget(in, args)
	if err != nil {
		t.Fatal(err)
	}
	cues := got.(map[string]any)["cues"].([]map[string]any)
	if len(cues) != 2 || cues[0]["text"] != "訳 3"+splitMarker+"訳 4" || cues[1]["text"] != "訳 5" {
		t.Errorf("cues = %v", cues)
	}
	// 系統指示沒有宣告、或同時符合多個目標時是錯誤（產品的 OutputRules 改了文字時要看得見地失敗）。
	for _, prefix := range []string{"沒有宣告", "目標語言：ja。目標語言：zh-Hant-TW。"} {
		in.Prefix = prefix
		if _, err := runByTarget(in, args); err == nil || !strings.Contains(err.Error(), "目標語言") {
			t.Errorf("prefix %q：err = %v", prefix, err)
		}
	}
	if err := checkByTargetArgs(json.RawMessage(`{"texts": {}}`)); err == nil {
		t.Error("texts 為空時要拒絕")
	}
	if err := checkByTargetArgs(json.RawMessage(`{"texts": {"ja": "x"}, "other": 1}`)); err == nil {
		t.Error("不認識的欄位要拒絕")
	}
}
