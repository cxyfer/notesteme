package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
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

// Compose 依 D5 的順序合併各層並驗證（沿用 BuildEffective：內建預設 → Global → Profile → Channel → Item → Run），
// 再依 D11 為會執行的 LLM 步驟解析 prompt 引用，把實際用到的版本寫進快照該步驟的 prompt_version、prompt_sha256。
//
// p.prompts 是 nil 時不解析（快照與沒有 prompt 庫時相同）；解析失敗時留空，不讓新增失敗（執行時才讓該步驟失敗）。
func (p *Pipeline) Compose(ctx context.Context, l Layers) (Effective, error) {
	eff, err := BuildEffective(l.Global, l.Profile.Config, l.Channel.Overrides, l.Item, l.Run)
	if err != nil {
		return nil, err
	}
	if p.prompts == nil {
		return eff, nil
	}
	return p.resolveStagePrompts(ctx, eff)
}

// resolveStagePrompts 只為會執行的 LLM 步驟（executor 是 api 或 agent，而且不是 skipped）解析 prompt（D11）。
func (p *Pipeline) resolveStagePrompts(ctx context.Context, eff Effective) (Effective, error) {
	stages, ok := cloneValue(eff["stages"]).(map[string]any)
	if !ok {
		return eff, nil
	}
	for _, stage := range Stages {
		if !IsLLMStage(stage) && stage != StageChapters {
			continue
		}
		res, err := ResolveExecutor(eff, stage, StageContext{})
		if err != nil {
			return nil, err
		}
		// skipped 與 opencc 都不會用到 prompt。
		if res.Skip || res.Executor == ExecutorOpenCC || res.Prompt == "" {
			continue
		}
		entry, ok := stages[stage].(map[string]any)
		if !ok {
			continue
		}
		rp, err := p.prompts.ResolvePrompt(ctx, res.Prompt)
		if err != nil {
			// 引用不存在或版本不存在：留空，S3 執行時再讓該步驟以 permanent 失敗。
			p.log.Warn("解析 prompt 失敗，快照不記錄版本", "stage", stage, "prompt", res.Prompt, "error", err)
			continue
		}
		entry["prompt_version"] = rp.Version
		entry["prompt_sha256"] = rp.SHA256
	}
	out := make(Effective, len(eff))
	for k, v := range eff {
		out[k] = v
	}
	out["stages"] = stages
	return out, nil
}

// currentLayers 讀取目前的各層：全域（DB）、處理方案（profile 為空時用預設處理方案）、
// 頻道（channelID 為空時沒有頻道層）。處理方案不存在時回傳 *InvalidError，Fields 為 [{profile, …}]。
// item 與 run 是呼叫端給的影音層與重跑層（nil 視為空）。
func (p *Pipeline) currentLayers(ctx context.Context, r *store.Repo, profile, channelID string, item, run map[string]any) (Layers, error) {
	global, err := p.globalConfig(ctx)
	if err != nil {
		return Layers{}, err
	}
	pl, err := p.profileLayer(ctx, r, profile)
	if err != nil {
		return Layers{}, err
	}
	l := Layers{
		Global: global, Profile: pl, Channel: ChannelLayer{Overrides: map[string]any{}},
		Item: orEmpty(item), Run: orEmpty(run),
	}
	if channelID != "" {
		ch, err := r.ChannelByID(ctx, channelID)
		if err != nil {
			return Layers{}, err
		}
		ov, err := decodeOverrides(ch.Overrides)
		if err != nil {
			return Layers{}, err
		}
		l.Channel = ChannelLayer{ID: ch.ID, Overrides: orEmpty(ov)}
	}
	return l, nil
}

// profileLayer 讀出處理方案目前的版本；name 為空時用預設處理方案（D7）。不存在時回傳 path 為 profile 的 *InvalidError。
func (p *Pipeline) profileLayer(ctx context.Context, r *store.Repo, name string) (ProfileLayer, error) {
	if name == "" {
		var err error
		if name, err = p.DefaultProfileName(ctx); err != nil {
			return ProfileLayer{}, err
		}
	}
	pr, err := r.Profile(ctx, name)
	if err == nil {
		var pv store.ProfileVersion
		if pv, err = r.ProfileVersion(ctx, name, pr.CurrentVersion); err == nil {
			cfg, derr := decodeOverrides(json.RawMessage(pv.ConfigJSON))
			if derr != nil {
				return ProfileLayer{}, derr
			}
			return ProfileLayer{Name: name, Version: pv.Version, SHA256: pv.SHA256, Config: orEmpty(cfg)}, nil
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		return ProfileLayer{}, invalidFields([]validate.FieldError{{Path: "profile", Message: "找不到處理方案「" + name + "」。"}})
	}
	return ProfileLayer{}, err
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// validateDeps 是驗證設定層時查詢 provider、prompt、vault 與 hook 用的依賴（nil 的欄位略過對應的檢查）。
// 處理方案、新增與重跑的覆寫、頻道覆寫、全域區段與 config import 都經過這裡，行為一致。
func (p *Pipeline) validateDeps() ValidateDeps {
	return ValidateDeps{Providers: p.providers, Prompts: p.prompts, Outputs: p.outputs}
}

// validateOverrides 驗證輸入的單次覆寫（新增、重跑、頻道覆寫），有錯誤時回傳 *InvalidError。
func (p *Pipeline) validateOverrides(ctx context.Context, doc map[string]any) error {
	if fs := ValidateLayer(ctx, doc, KindOverride, p.validateDeps()); len(fs) > 0 {
		return invalidFields(fs)
	}
	return nil
}

// rebindChannel 在 metadata 完成、頻道確定之後，把頻道層併進還沒開始的下游步驟（D6）。
// meta 是剛完成的 metadata stage run；latest 是交易開始時載入的各步驟最新的 stage run，
// 被改綁的那幾筆會同步更新它們的 ConfigSnapshotID：後面前進時依 latest 讀快照，漏掉就會用舊快照。
//
// 要處理的快照不只 metadata 自己的：metadata 還沒完成時從下游步驟重跑（Rerun），新建的 run 綁的是另一份
// 沒有頻道層的快照，每一份有還沒開始的最新步驟綁定的快照都要各自併入頻道層（Codex P2）。
//
// 以下情況什麼都不做：影音沒有頻道、快照沒有記錄各層（M1 的舊快照）、頻道和快照中記的相同、
// 重新 Compose 失敗（只記 log，保留舊快照，不讓 metadata 失敗）。在呼叫端的交易內執行。
func (p *Pipeline) rebindChannel(ctx context.Context, r *store.Repo, itemID string, meta store.StageRun, latest map[string]store.StageRun) error {
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return err
	}
	if it.ChannelID == "" {
		return nil
	}
	from := []string{meta.ConfigSnapshotID}
	var others []string
	for _, run := range latest {
		if run.Stage == StageMetadata || run.Status != store.StatusPending || run.StartedAt != nil {
			continue
		}
		if run.ConfigSnapshotID != meta.ConfigSnapshotID && !slices.Contains(others, run.ConfigSnapshotID) {
			others = append(others, run.ConfigSnapshotID)
		}
	}
	slices.Sort(others) // map 的走訪順序不固定；排序讓結果可重現
	from = append(from, others...)
	for _, fromID := range from {
		if err := p.rebindSnapshotChannel(ctx, r, it, fromID, latest); err != nil {
			return err
		}
	}
	return nil
}

// rebindSnapshotChannel 把頻道層併進 fromID 這份快照，並把綁在它上面、還沒開始的下游步驟改綁到新快照。
func (p *Pipeline) rebindSnapshotChannel(ctx context.Context, r *store.Repo, it store.Item, fromID string, latest map[string]store.StageRun) error {
	raw, err := r.SnapshotLayers(ctx, fromID)
	if err != nil {
		return err
	}
	var l Layers
	if raw == "" || raw == "{}" || json.Unmarshal([]byte(raw), &l) != nil || l.Profile.Name == "" {
		return nil
	}
	if it.ChannelID == l.Channel.ID {
		return nil
	}
	ch, err := r.ChannelByID(ctx, it.ChannelID)
	if err != nil {
		return err
	}
	ov, err := decodeOverrides(ch.Overrides)
	if err != nil {
		return err
	}
	l.Channel = ChannelLayer{ID: ch.ID, Overrides: orEmpty(ov)}
	eff, err := p.Compose(ctx, l)
	if err != nil {
		p.log.Warn("加入頻道層後的設定不正確，保留原本的快照", "item", it.ID, "channel", ch.ID, "error", err)
		return nil
	}
	sha, cfgJSON, layersJSON, err := SnapshotOf(eff, l)
	if err != nil {
		return err
	}
	newID, err := r.EnsureSnapshotLayers(ctx, sha, cfgJSON, layersJSON)
	if err != nil {
		return err
	}
	if newID == fromID {
		return nil
	}
	ids, err := r.RebindPendingRuns(ctx, it.ID, fromID, newID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		for stage, run := range latest {
			if run.ID == id {
				run.ConfigSnapshotID = newID
				latest[stage] = run
			}
		}
	}
	return nil
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
