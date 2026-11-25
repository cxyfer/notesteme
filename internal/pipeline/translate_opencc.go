package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

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
// 輸入依 translateSource 選（由輸入衍生的 edited > corrected > segmented > normalized）。與原文相同的目標略過；
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
	src, ok, err := translateSource(ctx, p.st.Read(), it.ID, "")
	if err != nil {
		return result{}, err
	}
	if !ok {
		return result{}, errPermanent(errNoSource.Error())
	}
	tr, err := p.readTranscriptArtifact(ctx, src)
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

	// 沒有需要轉換的目標（執行時才發現全都與原文相同，例如排入之後改了 language.targets）：
	// 與排入前就判斷出來的 same_language 一樣略過，並撤下不再要求的現行譯文（Codex P2）。
	if len(jobs) == 0 {
		impl := p.implVersion(StageTranslate)
		return result{
			task: taskDone, advance: true,
			stage:     &store.StageResult{Executor: ExecutorOpenCC, ImplVersion: impl},
			state:     skippedState(SkipSameLanguage),
			supersede: translatedSupersede(removedTranslatedLangs(arts, jobs)),
		}, nil
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

	// 這次不再要求、但仍是現行版本的 translated 產出（language.targets 減少了語言，或同一語言的拼法不同）：隨結果一起撤下（Codex P2）。
	removed := removedTranslatedLangs(arts, jobs)

	// 同一份上游、同一組目標與字典版本的結果已經是現行版本：重用，不建立新版本（PIPE-R5）。
	// 只看現行版本：設定 A → B → A 時不會恢復舊版，而是重新轉換（opencc 不花錢）。
	if first, hit := p.currentOpenCCOutputs(arts, jobs, key); hit {
		stage.ArtifactID = first
		return result{task: taskDone, advance: true, stage: stage, state: done, supersede: translatedSupersede(removed)}, nil
	}

	var out []*store.NewArtifact
	discard := func() {
		for _, a := range out {
			p.discardArtifactFile(ctx, a)
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
	return result{task: taskDone, advance: true, stage: stage, artifacts: out, state: done, supersede: translatedSupersede(removed)}, nil
}

// removedTranslatedLangs 列出現行的 translated 產出中，語言不在這次目標（jobs）裡的語言。
// 比對的是正規化後的拼法（openccJob.lang）：拼法不同的等價標記（zh-hant-tw）也算不在其中，會被撤下並以正規化的拼法重新產生。
func removedTranslatedLangs(arts []store.Artifact, jobs []openccJob) []string {
	var removed []string
	for _, a := range arts {
		if a.Kind != "transcript" || a.Variant != string(subtitle.VariantTranslated) {
			continue
		}
		keep := slices.ContainsFunc(jobs, func(j openccJob) bool { return a.Lang == j.lang })
		if !keep && !slices.Contains(removed, a.Lang) {
			removed = append(removed, a.Lang)
		}
	}
	return removed
}

func translatedSupersede(langs []string) *supersedeSet {
	if len(langs) == 0 {
		return nil
	}
	return &supersedeSet{Kind: "transcript", Variant: string(subtitle.VariantTranslated), Langs: langs}
}

// currentOpenCCOutputs 檢查每個目標是否都已有現行的 translated 產出，來源紀錄的 cache_key 與這次相符，
// 而且產出檔還在、sha256 相符（NFR-REL-2：損毀或遺失的檔案視為快取未命中，重新轉換）。
// 都有時回傳第一個目標的產出 ID（stage run 指向它）。
func (p *Pipeline) currentOpenCCOutputs(arts []store.Artifact, jobs []openccJob, key string) (firstID string, ok bool) {
	for i, j := range jobs {
		found := false
		for _, a := range arts {
			if a.Kind != "transcript" || a.Variant != string(subtitle.VariantTranslated) || a.Lang != j.lang {
				continue
			}
			var prov struct {
				CacheKey string `json:"cache_key"`
			}
			if json.Unmarshal(a.Provenance, &prov) == nil && prov.CacheKey == key+"/"+j.lang {
				if _, err := readTranscript(p.dataDir, a.Path, a.SHA256); err != nil {
					break // 檔案損毀或遺失：這個目標未命中
				}
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
