package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/validate"
	"github.com/cxyfer/notesteme/internal/zhconv"
)

// 詞彙表的上限：詞彙表會整份放進每個請求的固定前綴（COR-R4），不能無限制地成長。
const (
	MaxGlossaryTerms     = 500
	MaxGlossaryTermRunes = 100
	// maxTermCandidatesPerChannel 是一個頻道最多累積的術語候選（任何狀態）：候選來自模型的輸出，
	// 惡意字幕不能靠它讓資料庫無限制成長。
	maxTermCandidatesPerChannel = 500
)

// 詞彙表 API 的範圍；與 store 的 scope 相同。
const (
	GlossaryScopeGlobal  = store.GlossaryGlobal
	GlossaryScopeChannel = store.GlossaryChannel
)

// ErrGlossaryScope 表示詞彙表的 scope 不是 global 或 channel。
var ErrGlossaryScope = errors.New("詞彙表的範圍只能是 global 或 channel")

// NormalizeGlossaryTerms 檢查並整理使用者提供的詞彙表：去掉前後空白、不可以有空字串或控制字元、
// 每個詞最多 MaxGlossaryTermRunes 個字元、最多 MaxGlossaryTerms 個；大小寫相同的重複只留第一個。
func NormalizeGlossaryTerms(terms []string) ([]string, error) {
	var bad []validate.FieldError
	if len(terms) > MaxGlossaryTerms {
		bad = append(bad, validate.FieldError{Path: "terms", Message: fmt.Sprintf("最多 %d 個詞。", MaxGlossaryTerms)})
		return nil, invalidFields(bad)
	}
	out := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for i, t := range terms {
		t = strings.TrimSpace(t)
		path := fmt.Sprintf("terms[%d]", i)
		switch {
		case t == "":
			bad = append(bad, validate.FieldError{Path: path, Message: "不可以是空字串。"})
		case utf8.RuneCountInString(t) > MaxGlossaryTermRunes:
			bad = append(bad, validate.FieldError{Path: path, Message: fmt.Sprintf("最多 %d 個字元。", MaxGlossaryTermRunes)})
		case strings.ContainsFunc(t, unicode.IsControl):
			bad = append(bad, validate.FieldError{Path: path, Message: "不可以含控制字元（包含換行）。"})
		case !seen[strings.ToLower(t)]:
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	if len(bad) > 0 {
		return nil, invalidFields(bad)
	}
	return out, nil
}

// mergeTerms 依序合併詞彙表，大小寫相同的重複只留第一次出現的寫法。
func mergeTerms(lists ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lists {
		for _, t := range l {
			if k := strings.ToLower(t); t != "" && !seen[k] {
				seen[k] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// glossaryTerms 合併影音適用的詞彙表（COR-R4）：全域、頻道（詞彙表 API）、處理方案與頻道覆寫的 glossary。
// 影片標題與說明、章節名稱本來就在 <video_metadata> 中，不重複放進詞彙表。
func (p *Pipeline) glossaryTerms(ctx context.Context, it store.Item, eff Effective) ([]string, error) {
	r := p.st.Read()
	global, err := r.Glossary(ctx, store.GlossaryGlobal, store.GlobalGlossaryID)
	if err != nil {
		return nil, err
	}
	var channel store.Glossary
	if it.ChannelID != "" {
		if channel, err = r.Glossary(ctx, store.GlossaryChannel, it.ChannelID); err != nil {
			return nil, err
		}
	}
	return mergeTerms(global.Terms, channel.Terms, eff.Glossary()), nil
}

// glossaryForSource 把詞彙表轉成原文的字形（COR-R4、S6-Q2）：原文是中文時用 zhconv.GlossaryTable 的轉換表
// （簡體 tw2sp、台灣繁體 s2twp…）；原文標記沒有簡繁（例如 zh）時依字幕內容判斷（withDetectedScript）；
// 不是中文或無法判斷時原樣使用。
func glossaryForSource(terms []string, srcLang string, cues []subtitle.Cue) []string {
	if len(terms) == 0 || !langtag.IsChinese(srcLang) {
		return terms
	}
	if langtag.ChineseScript(srcLang) == "" {
		srcLang = withDetectedScript(srcLang, cues)
	}
	table, ok := zhconv.GlossaryTable(srcLang)
	if !ok {
		return terms
	}
	out := make([]string, len(terms))
	for i, t := range terms {
		c, err := zhconv.Convert(table, t)
		if err != nil {
			c = t
		}
		out[i] = c
	}
	return mergeTerms(out)
}

// resolveGlossaryOwner 把 API 的 scope 與 id 換成 store 的 (scope, scope_id)。
// 全域詞彙表的 id 固定是 default；頻道的 id 是頻道的 UUID 或 <platform>:<external_id>（同頻道覆寫，D14）；
// create 為 true 時，找不到的 natural key 會建立頻道。頻道還不存在而且不建立時，scope_id 是空字串（內容是空的）。
func (p *Pipeline) resolveGlossaryOwner(ctx context.Context, r *store.Repo, scope, id string, create bool) (scopeID string, err error) {
	switch scope {
	case store.GlossaryGlobal:
		if id != store.GlobalGlossaryID {
			return "", store.ErrNotFound
		}
		return store.GlobalGlossaryID, nil
	case store.GlossaryChannel:
		return p.resolveChannelID(ctx, r, id, create)
	}
	return "", ErrGlossaryScope
}

// resolveChannelID 回傳頻道的 UUID；UUID 找不到回傳 store.ErrNotFound；natural key 找不到時，
// create 為 true 就建立，否則回傳空字串。
func (p *Pipeline) resolveChannelID(ctx context.Context, r *store.Repo, key string, create bool) (string, error) {
	platform, externalID, natural := splitChannelKey(key)
	if !natural {
		ch, err := r.ChannelByID(ctx, key)
		return ch.ID, err
	}
	if err := checkChannelKey(platform, externalID); err != nil {
		return "", err
	}
	ch, err := r.ChannelByKey(ctx, platform, externalID)
	if errors.Is(err, store.ErrNotFound) {
		if !create {
			return "", nil
		}
		return r.UpsertChannel(ctx, store.Channel{Platform: platform, ExternalID: externalID})
	}
	return ch.ID, err
}

// Glossary 讀取詞彙表（COR-R4）；還沒有儲存過、或頻道還不存在時 Terms 是空的。
func (p *Pipeline) Glossary(ctx context.Context, scope, id string) (store.Glossary, error) {
	r := p.st.Read()
	scopeID, err := p.resolveGlossaryOwner(ctx, r, scope, id, false)
	if err != nil {
		return store.Glossary{}, err
	}
	if scopeID == "" {
		return store.Glossary{Scope: scope, ScopeID: id, Terms: []string{}}, nil
	}
	return r.Glossary(ctx, scope, scopeID)
}

// PutGlossary 整份取代詞彙表；詞彙表在每個請求的固定前綴中，所以內容先檢查並整理（NormalizeGlossaryTerms）。
func (p *Pipeline) PutGlossary(ctx context.Context, scope, id string, terms []string) (store.Glossary, error) {
	terms, err := NormalizeGlossaryTerms(terms)
	if err != nil {
		return store.Glossary{}, err
	}
	var out store.Glossary
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		scopeID, err := p.resolveGlossaryOwner(ctx, r, scope, id, true)
		if err != nil {
			return err
		}
		if err := r.PutGlossary(ctx, scope, scopeID, terms); err != nil {
			return err
		}
		out, err = r.Glossary(ctx, scope, scopeID)
		return err
	})
	return out, err
}

// TermCandidates 列出頻道的術語候選（COR-R6）；頻道還不存在時是空的。
func (p *Pipeline) TermCandidates(ctx context.Context, channelKey string) ([]store.TermCandidate, error) {
	r := p.st.Read()
	id, err := p.resolveChannelID(ctx, r, channelKey, false)
	if err != nil || id == "" {
		return []store.TermCandidate{}, err
	}
	return r.TermCandidates(ctx, id)
}
