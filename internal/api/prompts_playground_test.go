package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
)

// echoLLM 是假的 provider：每句加一個驚嘆號，用量固定。
type echoLLM struct{ calls int }

func (e *echoLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	e.calls++
	var data struct {
		Cues []llm.Cue `json:"cues"`
	}
	raw := req.Data[len(llm.DataOpen) : len(req.Data)-len(llm.DataClose)]
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return llm.Response{}, err
	}
	for i := range data.Cues {
		data.Cues[i].Text += "!"
	}
	out, _ := json.Marshal(map[string]any{"cues": data.Cues, "term_candidates": []any{}})
	return llm.Response{Output: out, Usage: llm.Usage{InputTokens: 100, OutputTokens: 20}}, nil
}

// promptsAdapter 把 prompts.Service 轉成 pipeline.PromptResolver（app 的 promptResolver 在另一個套件）。
type promptsAdapter struct{ s *prompts.Service }

func (a promptsAdapter) ResolvePrompt(ctx context.Context, ref string) (pipeline.ResolvedPrompt, error) {
	r, err := a.s.Resolve(ctx, ref)
	if err != nil {
		return pipeline.ResolvedPrompt{}, err
	}
	return pipeline.ResolvedPrompt{ID: r.ID, Kind: r.Kind, Version: r.Version, SHA256: r.SHA256, Body: r.Body}, nil
}

type playgroundRig struct {
	*itemsRig
	fake *echoLLM
	reg  *llm.Registry
	id   string
}

// newPlaygroundRig 建立有 prompt 庫與 LLM（假 provider、有 API key）的 rig，影音已處理到 normalize（3 句，各 2 秒）。
func newPlaygroundRig(t *testing.T) *playgroundRig {
	t.Helper()
	pr := &playgroundRig{fake: &echoLLM{}}
	pr.itemsRig = newItemsRigAPI(t, func(d *Deps) {
		d.Prompts = prompts.New(prompts.Deps{Store: d.Store, Clock: d.Clock})
	}, func(d *pipeline.Deps) {
		pr.reg = llm.NewRegistry(nil, d.Clock)
		pr.reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
		pr.reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return pr.fake }
		d.LLM = pr.reg
		d.Prompts = promptsAdapter{prompts.New(prompts.Deps{Store: d.Store, Clock: d.Clock})}
	})
	if rec := pr.owner("POST", "/api/prompts", `{"id":"my-correct","kind":"correct","body":"你是字幕校對員。\n詞彙表：{{glossary}}"}`); rec.Code != http.StatusCreated {
		t.Fatalf("建立 prompt = %d %s", rec.Code, rec.Body)
	}
	if rec := pr.owner("POST", "/api/prompts/my-correct/versions", `{"body":"新的版本 {{glossary}}"}`); rec.Code != http.StatusCreated {
		t.Fatalf("新增版本 = %d %s", rec.Code, rec.Body)
	}
	rec := pr.owner("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("新增 = %d %s", rec.Code, rec.Body)
	}
	pr.id = decode[itemsBody](t, rec).Items[0].ID
	if err := pr.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pr
}

const playgroundBodyJSON = `{"prompt":"my-correct@1","item":"yt:abc123xxxxx","from":"00:00:02","to":"00:00:06","provider":"anthropic","model":"fixture-model"}`

// PRM-R6：回傳輸出、驗證結果與預估花費（沒有價格時 null，但欄位存在）。
func TestPlaygroundRoute(t *testing.T) {
	r := newPlaygroundRig(t)
	rec := r.owner("POST", "/api/prompts/playground", playgroundBodyJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if string(raw["estimate_usd"]) != "null" {
		t.Errorf("estimate_usd = %s，預期欄位存在且是 null", raw["estimate_usd"])
	}
	body := decode[PlaygroundBody](t, rec)
	if body.Stage != "correct" || body.Prompt.ID != "my-correct" || body.Prompt.Version != 1 || body.Cues != 2 ||
		!body.Validation.Passed || len(body.Validation.Errors) != 0 || body.Usage.InputTokens == 0 {
		t.Errorf("回應 = %+v", body)
	}
	if len(body.Output) != 2 || body.Output[0].Text != "two!" || body.Output[0].Original != "two" || body.Output[1].Text != "three!" {
		t.Errorf("輸出 = %+v", body.Output)
	}
	// 試跑不寫入：影音詳情沒有出現 correct 的產出。
	rec = r.owner("GET", "/api/items/"+r.id, "")
	var it struct {
		Stages []struct{ Stage, Status string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &it)
	for _, s := range it.Stages {
		if s.Stage == "correct" && s.Status == "done" {
			t.Errorf("試跑不能讓步驟變成 done：%+v", it.Stages)
		}
	}
}

func TestPlaygroundRouteErrors(t *testing.T) {
	r := newPlaygroundRig(t)
	calls := r.fake.calls
	cases := []struct {
		name, body string
		status     int
		code, path string
	}{
		{"時間格式不正確", `{"prompt":"my-correct@1","item":"yt:abc123xxxxx","from":"soon","to":"00:00:06","provider":"anthropic","model":"m"}`, 422, CodeInvalidInput, "from"},
		{"範圍沒有字幕", `{"prompt":"my-correct@1","item":"yt:abc123xxxxx","from":"01:00:00","to":"01:01:00","provider":"anthropic","model":"m"}`, 422, CodeInvalidInput, "from"},
		{"沒有 model", `{"prompt":"my-correct@1","item":"yt:abc123xxxxx","from":"00:00:00","to":"00:00:06","provider":"anthropic"}`, 422, CodeInvalidInput, "model"},
		{"找不到影音", `{"prompt":"my-correct@1","item":"yt:nope00xxxxx","from":"00:00:00","to":"00:00:06","provider":"anthropic","model":"m"}`, 404, CodeNotFound, ""},
		{"找不到提示詞版本", `{"prompt":"my-correct@9","item":"yt:abc123xxxxx","from":"00:00:00","to":"00:00:06","provider":"anthropic","model":"m"}`, 404, CodeNotFound, ""},
	}
	for _, c := range cases {
		rec := r.owner("POST", "/api/prompts/playground", c.body)
		e := decodeError(t, rec.Body.Bytes())
		if rec.Code != c.status || e.Code != c.code || (c.path != "" && (len(e.Errors) == 0 || e.Errors[0].Path != c.path)) {
			t.Errorf("%s：%d %s", c.name, rec.Code, rec.Body)
		}
	}
	if r.fake.calls != calls {
		t.Error("輸入不正確時不能呼叫 provider")
	}

	// 缺 API key：409 needs_action，帶動作。
	r.reg.Env = map[string]string{}
	rec := r.owner("POST", "/api/prompts/playground", playgroundBodyJSON)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != http.StatusConflict || e.Code != CodeNeedsAction || len(e.Actions) == 0 || e.Message == "" {
		t.Errorf("缺 key：%d %s", rec.Code, rec.Body)
	}
}

// 試跑與比較要 ingest scope，而且在讀 body 之前檢查。
func TestPlaygroundScopes(t *testing.T) {
	r := newPlaygroundRig(t)
	for _, path := range []string{"/api/prompts/playground", "/api/prompts/my-correct/compare"} {
		rec := r.do("POST", path, "{", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s 沒有身分 = %d", path, rec.Code)
		}
	}
}

// PRM-R7：內文的行級差異；指定影音與範圍時另外回傳兩個版本在同一段字幕上的輸出。
func TestCompareRoute(t *testing.T) {
	r := newPlaygroundRig(t)
	rec := r.owner("POST", "/api/prompts/my-correct/compare", `{"a":1,"b":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		A, B     int
		BodyDiff []PromptDiffLine `json:"body_diff"`
		Outputs  json.RawMessage
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.A != 1 || out.B != 2 || len(out.BodyDiff) == 0 || out.Outputs != nil {
		t.Errorf("沒有指定影音：%s", rec.Body)
	}
	var deletes, inserts int
	for _, l := range out.BodyDiff {
		switch l.Op {
		case "delete":
			deletes++
		case "insert":
			inserts++
		}
	}
	if deletes != 2 || inserts != 1 {
		t.Errorf("diff = %+v，預期刪 2 行（兩行原文）、增 1 行", out.BodyDiff)
	}
	if r.fake.calls != 0 {
		t.Error("沒有指定影音時不能呼叫 provider")
	}

	rec = r.owner("POST", "/api/prompts/my-correct/compare",
		`{"a":1,"b":2,"item":"yt:abc123xxxxx","from":"00:00:00","to":"00:00:06","provider":"anthropic","model":"fixture-model"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var withOut struct{ Outputs CompareOutputs }
	_ = json.Unmarshal(rec.Body.Bytes(), &withOut)
	if withOut.Outputs.A.Prompt.Version != 1 || withOut.Outputs.B.Prompt.Version != 2 ||
		len(withOut.Outputs.A.Output) != 3 || len(withOut.Outputs.B.Output) != 3 {
		t.Errorf("outputs = %s", rec.Body)
	}
	if r.fake.calls != 2 {
		t.Errorf("兩個版本各試跑一次：provider 呼叫 %d 次", r.fake.calls)
	}

	// 版本不存在 404；指定影音卻沒有範圍 422。
	if rec := r.owner("POST", "/api/prompts/my-correct/compare", `{"a":1,"b":7}`); rec.Code != http.StatusNotFound {
		t.Errorf("版本不存在 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/prompts/my-correct/compare", `{"a":1,"b":2,"item":"yt:abc123xxxxx","provider":"anthropic","model":"m"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("沒有範圍 = %d %s", rec.Code, rec.Body)
	}
}
