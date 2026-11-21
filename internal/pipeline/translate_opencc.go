package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/zhconv"
)

// convertText 是測試接縫：runTranslateOpenCC 經這個變數轉換文字，單元測試可以換成假的對照表。
var convertText = zhconv.Convert

// openccJob 是一個要轉換的目標。
type openccJob struct {
	lang  string
	table zhconv.Table
}

// runTranslateOpenCC 用 OpenCC 把字幕轉成各目標語言的中文字形（TRN-R8）：不呼叫 LLM，不需要 provider。
// 一個 stage run 產生多份 translated 產出（每個目標語言一份，S6-Q4）；時間、index、timing、speaker 不變，
// 字詞級時間不保留（與之後的 LLM 翻譯一致）。
//
// 輸入依 translateInput 選（corrected > segmented > normalized）。與原文相同的目標略過；
// 需要翻譯的目標（非中文，或原文不是中文）以 permanent 失敗：opencc 只能轉換中文字形。
// 明確設定 executor: opencc 時，variant_strategy: localize 的目標也只做 OpenCC 轉換（用含詞彙轉換的 convert 表）。
func (p *Pipeline) runTranslateOpenCC(ctx context.Context, it store.Item, eff Effective) (result, error) {
	lang, err := eff.Language()
	if err != nil {
		return result{}, err
	}
	arts, err := p.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		return result{}, err
	}
	src, ok := translateInput(arts)
	if !ok {
		return result{}, errPermanent(errNoSource.Error())
	}
	tr, err := readTranscript(p.dataDir, src.Path, src.SHA256)
	if err != nil {
		if errors.Is(err, ErrCorruptArtifact) {
			return result{}, errPermanent(err.Error())
		}
		return result{}, err
	}
	sourceLang := src.Lang
	if sourceLang == "" {
		sourceLang = it.OriginalLang
	}
	if langtag.IsChinese(sourceLang) && langtag.ChineseScript(sourceLang) == "" {
		sourceLang = withDetectedScript(sourceLang, tr.Cues)
	}

	var jobs []openccJob
	for _, t := range PlanTargets(sourceLang, lang) {
		table := t.Table
		switch t.Kind {
		case TargetSame:
			continue
		case TargetLocalize:
			// 走到這裡代表明確設定了 opencc：只做確定性轉換，不再交給 LLM。
			var ok bool
			if table, ok = zhconv.TableFor(sourceLang, t.Lang, zhconv.StrategyConvert); !ok {
				continue
			}
		case TargetTranslate:
			what := "原文 " + sourceLang
			if !langtag.IsChinese(t.Lang) {
				what = "目標 " + t.Lang
			}
			return result{}, &errclass.Error{
				Class: errclass.ClassPermanent, Source: "pipeline", Detail: "opencc 不支援 " + what,
				Message: "opencc 只能轉換中文字形（" + what + "）。",
			}
		}
		jobs = append(jobs, openccJob{lang: t.Lang, table: table})
	}

	params := openccParams{Data: zhconv.DataVersion}
	for _, j := range jobs {
		params.Targets = append(params.Targets, openccTarget{Lang: j.lang, Table: j.table})
	}
	impl := p.implVersion(StageTranslate)
	key := cacheKey{
		Item: it.ID, Stage: StageTranslate, Impl: impl, Upstream: src.SHA256, Provider: "opencc", Params: paramsSHA(params),
	}.String()
	stage := &store.StageResult{Executor: ExecutorOpenCC, CacheKey: key, ImplVersion: impl}
	done := store.StageState{Status: store.StatusDone}

	// 同一份上游、同一組目標與字典版本的結果已經是現行版本：重用，不建立新版本（PIPE-R5）。
	// 只看現行版本：設定 A → B → A 時不會恢復舊版，而是重新轉換（opencc 不花錢）。
	if first, hit := currentOpenCCOutputs(arts, jobs, key); hit && len(jobs) > 0 {
		stage.ArtifactID = first
		return result{task: taskDone, advance: true, stage: stage, state: done}, nil
	}

	var out []*store.NewArtifact
	discard := func() {
		for _, a := range out {
			p.discardArtifactFile(a)
		}
	}
	for _, j := range jobs {
		cues := make([]subtitle.Cue, len(tr.Cues))
		for i, c := range tr.Cues {
			text, err := convertText(j.table, c.Text)
			if err != nil {
				discard()
				return result{}, fmt.Errorf("轉換 cue %d（%s）：%w", c.Index, j.table, err)
			}
			cues[i] = subtitle.Cue{Index: c.Index, StartMS: c.StartMS, EndMS: c.EndMS, Text: text, Timing: c.Timing, Speaker: c.Speaker}
		}
		prov, err := p.provenance(map[string]any{
			"stage": StageTranslate, "impl_version": impl, "executor": ExecutorOpenCC,
			"table": string(j.table), "strategy": string(zhconv.StrategyConvert), "opencc_data": zhconv.DataVersion,
			"source_lang": sourceLang, "target": j.lang,
			"derived_from": []map[string]string{{"artifact": src.ID, "variant": src.Variant, "sha256": src.SHA256}},
			"cache_key":    key + "/" + j.lang,
		}, nil)
		if err != nil {
			discard()
			return result{}, err
		}
		art, err := p.writeCuesArtifact(ctx, it.ID, subtitle.Transcript{
			Lang: j.lang, Variant: subtitle.VariantTranslated, Speakers: tr.Speakers, Cues: cues,
		}, []string{src.ID}, prov)
		if err != nil {
			discard()
			return result{}, err
		}
		out = append(out, art)
	}
	return result{task: taskDone, advance: true, stage: stage, artifacts: out, state: done}, nil
}

// currentOpenCCOutputs 檢查每個目標是否都已有現行的 translated 產出，而且來源紀錄的 cache_key 與這次相符。
// 都有時回傳第一個目標的產出 ID（stage run 指向它）。
func currentOpenCCOutputs(arts []store.Artifact, jobs []openccJob, key string) (firstID string, ok bool) {
	for i, j := range jobs {
		found := false
		for _, a := range arts {
			if a.Kind != "transcript" || a.Variant != string(subtitle.VariantTranslated) || !strings.EqualFold(a.Lang, j.lang) {
				continue
			}
			var prov struct {
				CacheKey string `json:"cache_key"`
			}
			if json.Unmarshal(a.Provenance, &prov) == nil && prov.CacheKey == key+"/"+j.lang {
				found = true
				if i == 0 {
					firstID = a.ID
				}
				break
			}
		}
		if !found {
			return "", false
		}
	}
	return firstID, len(jobs) > 0
}
