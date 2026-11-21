package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/cxyfer/notesteme/internal/store"
)

// Layers 是一份生效設定的組成（PRF-R2），原樣存進 config_snapshots.layers_json。
// 合併順序是：內建預設 → Global → Profile → Channel → Item → Run（決策 D5）。
type Layers struct {
	Global  map[string]any `json:"global"`
	Profile ProfileLayer   `json:"profile"`
	Channel ChannelLayer   `json:"channel"`
	Item    map[string]any `json:"item"`
	Run     map[string]any `json:"run"`
}

// ProfileLayer 是處理方案這一層。Config 內嵌處理方案的完整內容：處理方案之後被刪除或修改，也能重建快照（D3、D6）。
type ProfileLayer struct {
	Name    string         `json:"name"`
	Version int            `json:"version"`
	SHA256  string         `json:"sha256"`
	Config  map[string]any `json:"config"`
}

// ChannelLayer 是頻道這一層。
type ChannelLayer struct {
	// ID 是頻道的 ID；空字串代表建立快照時還不知道頻道（YouTube 要到 metadata 完成才知道，D6）。
	ID        string         `json:"id"`
	Overrides map[string]any `json:"overrides"`
}

// Compose 依 D5 的順序合併各層並驗證，結果和 BuildEffective(l.Global, l.Profile.Config, l.Channel.Overrides, l.Item, l.Run) 相同。
//
// TODO(WP-2)：依 D11 為會執行的 LLM 步驟解析 prompt，寫入 prompt_version、prompt_sha256。
// 契約階段只包住 BuildEffective，行為與 M2 S0 完全相同（Profile 與 Channel 層目前沒有呼叫端）。
func (p *Pipeline) Compose(ctx context.Context, l Layers) (Effective, error) {
	_ = ctx
	return BuildEffective(l.Global, l.Profile.Config, l.Channel.Overrides, l.Item, l.Run)
}

// currentLayers 讀取目前的各層：全域（DB）、處理方案（profile 為空時用預設處理方案）、
// 頻道（channelID 為空時沒有頻道層）。處理方案不存在時回傳 *InvalidError，Fields 為 [{profile, …}]。
//
// TODO(WP-2)：實作（M2 S1/S2 設計第 4.1 節）。
func (p *Pipeline) currentLayers(ctx context.Context, r *store.Repo, profile, channelID string, item, run map[string]any) (Layers, error) {
	_, _, _, _, _, _ = ctx, r, profile, channelID, item, run
	return Layers{}, &NotImplementedError{What: "處理方案的分層設定"}
}

// SnapshotOf 回傳快照的 sha256、config JSON 與 layers JSON。
// sha256 對 {"config":…,"layers":…} 的 JSON 計算（encoding/json 對 map 依 key 排序，相同的輸入得到相同的結果），
// 所以同樣的生效設定但來自不同的層，會是不同的快照。
func SnapshotOf(eff Effective, l Layers) (sha, configJSON, layersJSON string, err error) {
	cb, err := json.Marshal(map[string]any(eff))
	if err != nil {
		return "", "", "", err
	}
	lb, err := json.Marshal(l)
	if err != nil {
		return "", "", "", err
	}
	whole, err := json.Marshal(struct {
		Config json.RawMessage `json:"config"`
		Layers json.RawMessage `json:"layers"`
	}{cb, lb})
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256(whole)
	return hex.EncodeToString(sum[:]), string(cb), string(lb), nil
}
