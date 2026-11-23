package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// geminiSections 把 Gemini 的筆記換成 NOTE 渲染用的 sections（GEM-R5、R6）：沒有字幕，所以每一節與內文的每個
// [[t:MM:SS]] 各給一個虛擬的句號，cueStart 記下句號對應的時間；內文的時間引用換成 [[cue:N]]，交給 NOTE 的渲染換成時間連結。
// 輸入已通過 CheckGeminiNote（內文沒有 [[cue:N]]、時間都在片長內）。
func geminiSections(o llm.GeminiNoteOutput) (llm.NoteOutput, map[int]int64) {
	cueStart := map[int]int64{}
	next := len(o.Sections)
	out := llm.NoteOutput{SelfCheck: o.SelfCheck}
	for k, s := range o.Sections {
		cue := k + 1
		cueStart[cue] = s.StartMS
		body := s.Body
		spans, ms := llm.GeminiTimeRefs(body)
		// 從後面往前取代，前面的位置才不會移動。
		for i := len(spans) - 1; i >= 0; i-- {
			next++
			cueStart[next] = ms[i]
			body = body[:spans[i][0]] + fmt.Sprintf("[[cue:%d]]", next) + body[spans[i][1]:]
		}
		out.Sections = append(out.Sections, llm.NoteSection{Level: s.Level, Title: s.Title, Cue: cue, Body: body, Frames: []int{}})
	}
	return out, cueStart
}

// writeGeminiNote 以 NOTE 的模板、跳脫與自動檢查寫出 Gemini 的筆記（GEM-R6、NOTE-R7、R8、R11）：
// frontmatter 加上 notesteme_engine: gemini、notesteme_timing: model；來源紀錄記錄 engine、timing、provider、model、
// gemini.file_uri；自動檢查不通過時同 pipeline 引擎，寫出產出但步驟 blocked（needs_action）。
func (p *Pipeline) writeGeminiNote(ctx context.Context, it store.Item, eff Effective, n geminiNote) (result, error) {
	set, err := noteSettingsOf(eff)
	if err != nil {
		return result{}, err
	}
	tpl, err := p.resolveNoteTemplate(ctx, set.Template)
	if err != nil {
		return result{}, err
	}
	lang := noteLangOf(eff, set, it.OriginalLang)
	r := p.st.Read()
	status := notes.StatusComplete
	if degraded, err := p.noteDegraded(ctx, r, it.ID); err != nil {
		return result{}, err
	} else if degraded {
		status = notes.StatusPartial
	}
	out, cueStart := geminiSections(n.Output)
	in := p.noteInputFor(it, lang, status, subtitle.Transcript{}, nil, nil, nil)
	in.CueStart = cueStart
	in.SubtitleLink = p.vaultSubtitleLink(it, eff)
	in.Front.Engine, in.Front.Timing = "gemini", "model"
	doc, err := renderNote(in, out, tpl)
	if err != nil {
		var te *notes.TemplateError
		if errors.As(err, &te) {
			return result{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: te.Message,
				Message: "筆記模板無法套用：" + te.Message}
		}
		return result{}, err
	}
	doc.Engine = "gemini"
	body, err := json.Marshal(doc)
	if err != nil {
		return result{}, err
	}
	passed := notes.Passed(doc.Checks)
	fields := map[string]any{
		"stage": StageNote, "impl_version": p.implVersion(StageNote), "executor": ExecutorAPI,
		"engine": "gemini", "timing": "model", "provider": n.Provider, "model": n.Model,
		"gemini":   map[string]any{"file_uri": n.FileURI, "requests": n.Requests},
		"usage":    n.Usage,
		"template": tpl.Ref(), "template_sha256": tpl.SHA256, "checks": map[string]any{"passed": passed},
		"lang": lang, "note_status": status, "inputs": n.Parents,
	}
	// parents（GEM-R6）：metadata 不是產出，只記在 inputs；有片段時列出 sponsor_segments 的產出。
	var parents []string
	if slices.Contains(n.Parents, KindSponsorSegments) {
		if sb, ok, err := p.currentSponsorArtifact(ctx, it.ID); err != nil {
			return result{}, err
		} else if ok {
			parents = append(parents, sb.ID)
			fields["derived_from"] = []map[string]string{{"artifact": sb.ID, "variant": sb.Variant, "sha256": sb.SHA256}}
		}
	}
	prov, err := p.provenance(fields, nil)
	if err != nil {
		return result{}, err
	}
	id := store.NewID()
	rel, sha, size, err := writeArtifactFile(p.dataDir, it.ID, id, func(w io.Writer) error {
		_, werr := w.Write(body)
		return werr
	})
	if err != nil {
		return result{}, fmt.Errorf("寫入產出檔：%w", err)
	}
	art := &store.NewArtifact{
		ID: id, ItemID: it.ID, Kind: KindNote, Variant: KindNote, Lang: lang, Format: noteFmt,
		Path: rel, SHA256: sha, Size: size, Parents: parents, Provenance: prov,
	}
	stage := &store.StageResult{Executor: ExecutorAPI, ImplVersion: p.implVersion(StageNote)}
	state := store.StageState{Status: store.StatusDone}
	if !passed {
		state = store.StageState{
			Status: store.StatusBlocked, BlockReason: reasonNeedsAction, ErrorClass: string(errclass.ClassNeedsAction),
			Message: notes.FailureMessage(doc.Checks), Actions: []string{ActionRegenerateNote, actionOpenTemplate},
		}
	}
	return result{task: taskDone, advance: passed, stage: stage, artifact: art, state: state}, nil
}
