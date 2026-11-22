package steps

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakellm"
)

// registerLLM 註冊「LLM」的步驟，句型見 docs/specs/steps.md。
//
// 這些步驟只設定或讀取 fakellm（腳本化的 anthropic、openai，掛在 fakenet 上）：
// 步驟本身不判斷被測程式的行為，場景另有其他斷言把關（例如 `received 0 requests` 單獨成立不會讓場景通過）。
func registerLLM(r *Registry, s *Scenario) {
	r.Step(`the LLM provider {string} answers {string} with {string}`, s.llmAnswers)
	r.Step(`the LLM provider {string} answers {string} with {string} and then {string}`, s.llmAnswersThen)
	r.Step(`the LLM provider {string} answers {string} for chunk {int} with {string}`, s.llmAnswersChunk)
	r.Step(`the LLM provider {string} fails with HTTP {int}`, s.llmFails)
	r.Step(`the LLM provider {string} fails with HTTP {int} once`, s.llmFailsOnce)
	r.Step(`the LLM provider {string} received {int} requests`, s.llmReceived)
	r.Step(`the LLM provider {string} received {int} requests for {string}`, s.llmReceivedFor)
	r.Step(`request {int} to {string} contains {string}`, s.llmRequestContains)
	r.Step(`request {int} to {string} does not contain {string}`, s.llmRequestDoesNotContain)
	r.Step(`requests {int} and {int} to {string} share the same prefix before the chunk`, s.llmSharePrefix)
	r.Step(`the {string} chunks of {string} are:`, s.llmChunksAre)
}

// llmStageVariants 是 LLM 步驟產出的字幕變體（chunk 的切分記在這個產出的來源紀錄 chunks[]，LLM-R10）。
var llmStageVariants = map[string]string{"segment": "segmented", "correct": "corrected", "translate": "translated"}

// llmChunksAre 比對步驟現行產出的 chunk 切分：來源紀錄的 chunks[]，每項 {chunk, cues: [from, to], context: [from, to] 或 null}。
// 表格欄位 chunk、cues（"1-200"）、context（"193-197"，沒有前文時是 "none"）；chunk 數要與表格列數相同，並依序比對。
func (s *Scenario) llmChunksAre(stage, ref string, table *godog.Table) error {
	variant, ok := llmStageVariants[stage]
	if !ok {
		return fmt.Errorf("不認識的 LLM 步驟 %q（只有 segment、correct、translate）", stage)
	}
	want, err := tableMaps(table)
	if err != nil {
		return err
	}
	if s.stopped {
		return errServerStopped
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	if s.server == nil {
		return errServerStopped
	}
	rec, err := s.server.ArtifactProvenance(s.ctx, id, variant)
	if err != nil {
		return fmt.Errorf("%s 的 %q 產出：%w", ref, variant, err)
	}
	var doc struct {
		Chunks []struct {
			Chunk   int     `json:"chunk"`
			Cues    [2]int  `json:"cues"`
			Context *[2]int `json:"context"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(rec.Provenance, &doc); err != nil {
		return fmt.Errorf("%s 的 %q 產出：provenance 不是預期的 JSON：%w（%s）", ref, variant, err, snippet(rec.Provenance))
	}
	got := make([]map[string]string, 0, len(doc.Chunks))
	for _, c := range doc.Chunks {
		ctx := "none"
		if c.Context != nil {
			ctx = fmt.Sprintf("%d-%d", c.Context[0], c.Context[1])
		}
		got = append(got, map[string]string{"chunk": fmt.Sprint(c.Chunk), "cues": fmt.Sprintf("%d-%d", c.Cues[0], c.Cues[1]), "context": ctx})
	}
	if len(got) != len(want) {
		return fmt.Errorf("%s 的 %q 有 %d 個 chunk，預期 %d 個：%v", ref, stage, len(got), len(want), got)
	}
	for i, w := range want {
		for k, v := range w {
			g, ok := got[i][k]
			if !ok {
				return fmt.Errorf("不認識的欄位 %q（只有 chunk、cues、context）", k)
			}
			if g != v {
				return fmt.Errorf("%s 的 %q 第 %d 個 chunk 的 %s 是 %q，預期 %q（全部：%v）", ref, stage, i+1, k, g, v, got)
			}
		}
	}
	return nil
}

// llmProvider 檢查 provider 是 fakellm 模擬的 provider（anthropic、openai）。
func llmProvider(id string) error {
	if !fakellm.IsProvider(id) {
		return fmt.Errorf("不認識的 LLM provider %q（只有 anthropic、openai）", id)
	}
	return nil
}

func (s *Scenario) llmAnswers(provider, stage, fixture string) error {
	if err := llmProvider(provider); err != nil {
		return err
	}
	return s.World.LLM.Answer(provider, stage, fixture)
}

func (s *Scenario) llmAnswersThen(provider, stage, first, second string) error {
	if err := llmProvider(provider); err != nil {
		return err
	}
	return s.World.LLM.Answer(provider, stage, first, second)
}

func (s *Scenario) llmAnswersChunk(provider, stage string, chunk int, fixture string) error {
	if err := llmProvider(provider); err != nil {
		return err
	}
	return s.World.LLM.AnswerChunk(provider, stage, chunk, fixture)
}

// llmFail 讓 provider 的請求回傳 HTTP 錯誤；times 為 0 代表一直失敗。
func (s *Scenario) llmFail(provider string, status, times int) error {
	if err := llmProvider(provider); err != nil {
		return err
	}
	if status < 400 || status > 599 {
		return fmt.Errorf("HTTP %d 不是錯誤狀態碼（400-599）", status)
	}
	s.World.LLM.Fail(provider, status, times)
	return nil
}

func (s *Scenario) llmFails(provider string, status int) error {
	return s.llmFail(provider, status, 0)
}

func (s *Scenario) llmFailsOnce(provider string, status int) error {
	return s.llmFail(provider, status, 1)
}

// llmReceived 比對 provider 收到的請求總數（失敗的請求也算）。
func (s *Scenario) llmReceived(provider string, want int) error {
	if err := llmProvider(provider); err != nil {
		return err
	}
	reqs := s.World.LLM.Requests(provider)
	if len(reqs) != want {
		return fmt.Errorf("LLM provider %q 收到 %d 個請求，預期 %d 個%s", provider, len(reqs), want, describeRequests(reqs))
	}
	return nil
}

// llmReceivedFor 比對 provider 收到、屬於指定步驟的請求數（依 notesteme_<stage> 判斷）。
func (s *Scenario) llmReceivedFor(provider string, want int, stage string) error {
	if err := llmProvider(provider); err != nil {
		return err
	}
	reqs := s.World.LLM.Requests(provider)
	n := 0
	for _, r := range reqs {
		if r.Stage == stage {
			n++
		}
	}
	if n != want {
		return fmt.Errorf("LLM provider %q 收到 %d 個 %q 的請求，預期 %d 個%s", provider, n, stage, want, describeRequests(reqs))
	}
	return nil
}

// llmRequest 取出 provider 的第 n 個請求（從 1 起算，依抵達順序）；不存在時回傳錯誤。
func (s *Scenario) llmRequest(provider string, n int) (fakellm.Request, error) {
	if err := llmProvider(provider); err != nil {
		return fakellm.Request{}, err
	}
	reqs := s.World.LLM.Requests(provider)
	if n < 1 || n > len(reqs) {
		return fakellm.Request{}, fmt.Errorf("LLM provider %q 沒有第 %d 個請求（只收到 %d 個）%s", provider, n, len(reqs), describeRequests(reqs))
	}
	return reqs[n-1], nil
}

func (s *Scenario) llmRequestContains(n int, provider, text string) error {
	r, err := s.llmRequest(provider, n)
	if err != nil {
		return err
	}
	if !strings.Contains(r.Text, text) {
		return fmt.Errorf("對 %s 的第 %d 個請求（%s）不含 %q\n開頭：%s", provider, n, describeRequest(r), text, excerpt(r.Text))
	}
	return nil
}

// llmRequestDoesNotContain 要求請求存在：請求不存在時失敗，不能讓「不包含」恆真。
func (s *Scenario) llmRequestDoesNotContain(n int, provider, text string) error {
	r, err := s.llmRequest(provider, n)
	if err != nil {
		return err
	}
	if strings.Contains(r.Text, text) {
		return fmt.Errorf("對 %s 的第 %d 個請求（%s）不該含 %q", provider, n, describeRequest(r), text)
	}
	return nil
}

// llmSharePrefix 確認兩個請求的固定前綴相同。兩個請求都必須帶有 <transcript_data> 資料區塊，而且前綴不是空的，
// 否則「相同」沒有意義（凍結的資料區塊契約見 fakellm 的套件說明；S3 若改版面要同步）。
func (s *Scenario) llmSharePrefix(a, b int, provider string) error {
	ra, err := s.llmRequest(provider, a)
	if err != nil {
		return err
	}
	rb, err := s.llmRequest(provider, b)
	if err != nil {
		return err
	}
	for _, x := range []struct {
		n int
		r fakellm.Request
	}{{a, ra}, {b, rb}} {
		if !strings.Contains(x.r.Text, fakellm.DataOpen) || x.r.Chunk < 1 {
			return fmt.Errorf("對 %s 的第 %d 個請求（%s）沒有 %s 資料區塊，無法比對前綴", provider, x.n, describeRequest(x.r), fakellm.DataOpen)
		}
		if x.r.Prefix == "" {
			return fmt.Errorf("對 %s 的第 %d 個請求的資料區塊之前沒有任何內容", provider, x.n)
		}
	}
	if ra.Prefix != rb.Prefix {
		i := commonPrefixLen(ra.Prefix, rb.Prefix)
		return fmt.Errorf("請求 %d 與 %d 的固定前綴不同，從第 %d 個位元組開始：\n%d：…%s\n%d：…%s",
			a, b, i, a, excerpt(ra.Prefix[i:]), b, excerpt(rb.Prefix[i:]))
	}
	return nil
}

func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// excerpt 回傳字串開頭的 200 個字元（不切斷多位元組字元），失敗訊息用。
func excerpt(s string) string {
	const limit = 200
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

func describeRequest(r fakellm.Request) string {
	return fmt.Sprintf("stage=%q chunk=%d status=%d", r.Stage, r.Chunk, r.Status)
}

// describeRequests 列出請求的摘要，失敗訊息用（最多 10 個）。
func describeRequests(reqs []fakellm.Request) string {
	if len(reqs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n實際的請求：")
	for i, r := range reqs {
		if i == 10 {
			fmt.Fprintf(&sb, "\n  …（還有 %d 個）", len(reqs)-i)
			break
		}
		fmt.Fprintf(&sb, "\n  %d. %s", r.Seq, describeRequest(r))
	}
	return sb.String()
}

// tableMaps 把有表頭的表格轉成每列一個 map（欄位名稱 → 值，去掉前後空白）。
func tableMaps(t *godog.Table) ([]map[string]string, error) {
	if t == nil || len(t.Rows) < 2 {
		return nil, fmt.Errorf("表格至少要有表頭與一列")
	}
	header := make([]string, len(t.Rows[0].Cells))
	for i, c := range t.Rows[0].Cells {
		header[i] = strings.TrimSpace(c.Value)
	}
	out := make([]map[string]string, 0, len(t.Rows)-1)
	for _, row := range t.Rows[1:] {
		if len(row.Cells) != len(header) {
			return nil, fmt.Errorf("表格的欄數不一致")
		}
		m := make(map[string]string, len(header))
		for i, c := range row.Cells {
			m[header[i]] = strings.TrimSpace(c.Value)
		}
		out = append(out, m)
	}
	return out, nil
}
