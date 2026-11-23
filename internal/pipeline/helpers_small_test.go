package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// fakeFileInfo 是 MediaETag 用到的 os.FileInfo：只有 size 與 mtime 有意義。
type fakeFileInfo struct {
	fs.FileInfo
	size  int64
	mtime time.Time
}

func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) ModTime() time.Time { return f.mtime }

// MediaETag：上傳的副本不可變，有雜湊就用；用路徑加入的檔案只有 size 與 mtime 和記錄雜湊時相同才用雜湊，
// 否則回傳空字串（改用 Last-Modified），避免檔案被同一路徑替換後舊的 If-None-Match 得到 304。
func TestMediaETag(t *testing.T) {
	mtime := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	fi := fakeFileInfo{size: 1234, mtime: mtime}
	recorded := json.RawMessage(fmt.Sprintf(`{"local":{"size":1234,"mtime_ns":%d}}`, mtime.UnixNano()))
	path := store.Item{Origin: store.OriginManual, ContentSHA256: "abc", Metadata: recorded}

	cases := []struct {
		name string
		it   store.Item
		fi   fs.FileInfo
		want string
	}{
		{"沒有雜湊", store.Item{Origin: store.OriginUpload}, fi, ""},
		{"上傳的副本不看 stat", store.Item{Origin: store.OriginUpload, ContentSHA256: "abc"}, nil, `"abc"`},
		{"路徑加入且 stat 相同", path, fi, `"abc"`},
		{"路徑加入但 size 不同", path, fakeFileInfo{size: 1, mtime: mtime}, ""},
		{"路徑加入但 mtime 不同", path, fakeFileInfo{size: 1234, mtime: mtime.Add(time.Second)}, ""},
		{"路徑加入但沒有 FileInfo", path, nil, ""},
		{"路徑加入但沒有記錄 stat", store.Item{Origin: store.OriginManual, ContentSHA256: "abc"}, fi, ""},
	}
	for _, c := range cases {
		if got := MediaETag(c.it, c.fi); got != c.want {
			t.Errorf("%s：%q，預期 %q", c.name, got, c.want)
		}
	}
}

// COR-R6：核可把詞加進頻道詞彙表並標記 accepted；同樣的決定再送一次不動作（冪等）；
// 已決定成另一個結果時 ErrTermCandidateDecided；拒絕不動詞彙表；state 不合法或頻道不存在是錯誤。
func TestDecideTermCandidate(t *testing.T) {
	r := newRig(t)
	r.yt.info = youtube.Info{ID: "abc123xxxxx", Title: "T", Language: "en", ChannelID: "UC1", Channel: "ch"}
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	ctx := context.Background()
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	it, err := r.st.Read().ItemByID(ctx, it.ID)
	if err != nil || it.ChannelID == "" {
		t.Fatalf("item = %+v %v", it, err)
	}
	add := func(term string) string {
		t.Helper()
		if ok, err := r.st.Write().AddTermCandidate(ctx, it.ChannelID, it.ID, term); err != nil || !ok {
			t.Fatalf("加入候選 %q：%v %v", term, ok, err)
		}
		cands, err := r.p.TermCandidates(ctx, it.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cands {
			if c.Term == term {
				return c.ID
			}
		}
		t.Fatalf("找不到候選 %q", term)
		return ""
	}
	glossary := func() []string {
		g, err := r.p.Glossary(ctx, "channel", it.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		return g.Terms
	}

	kraft, nope := add("KRaft"), add("Nope")
	got, err := r.p.DecideTermCandidate(ctx, it.ChannelID, kraft, store.TermCandidateAccepted)
	if err != nil || got.State != store.TermCandidateAccepted || got.Term != "KRaft" {
		t.Fatalf("核可 = %+v %v", got, err)
	}
	if g := glossary(); len(g) != 1 || g[0] != "KRaft" {
		t.Errorf("詞彙表 = %v", g)
	}
	// 冪等：同樣的決定不重複加入。
	if _, err := r.p.DecideTermCandidate(ctx, it.ChannelID, kraft, store.TermCandidateAccepted); err != nil || len(glossary()) != 1 {
		t.Errorf("重複核可：%v，詞彙表 %v", err, glossary())
	}
	if _, err := r.p.DecideTermCandidate(ctx, it.ChannelID, kraft, store.TermCandidateRejected); !errors.Is(err, ErrTermCandidateDecided) {
		t.Errorf("改成另一個結果：%v", err)
	}
	// 拒絕：不動詞彙表，候選仍在表中（不會再被提出）。
	got, err = r.p.DecideTermCandidate(ctx, it.ChannelID, nope, store.TermCandidateRejected)
	if err != nil || got.State != store.TermCandidateRejected || len(glossary()) != 1 {
		t.Errorf("拒絕 = %+v %v，詞彙表 %v", got, err, glossary())
	}
	if cands, _ := r.p.TermCandidates(ctx, it.ChannelID); len(cands) != 2 {
		t.Errorf("候選 = %+v", cands)
	}
	var inv *InvalidError
	if _, err := r.p.DecideTermCandidate(ctx, it.ChannelID, kraft, "maybe"); !errors.As(err, &inv) {
		t.Errorf("不合法的 state：%v", err)
	}
	if _, err := r.p.DecideTermCandidate(ctx, "youtube:UCnone", kraft, store.TermCandidateAccepted); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("頻道不存在：%v", err)
	}
	if _, err := r.p.DecideTermCandidate(ctx, it.ChannelID, "missing", store.TermCandidateAccepted); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("候選不存在：%v", err)
	}
}

// 錯誤型別的訊息與 Unwrap：API 用 errors.Is／As 對應狀態碼，包裝後仍要認得原始錯誤。
func TestErrorTypesWrapTheirCause(t *testing.T) {
	cause := errors.New("boom")
	up := &UpstreamError{Msg: "上游失敗", Err: cause}
	if up.Error() != "上游失敗" || !errors.Is(fmt.Errorf("x: %w", up), cause) {
		t.Errorf("UpstreamError = %q", up.Error())
	}
	if msg := (&NotImplementedError{What: "frames"}).Error(); msg != "這個功能尚未實作：frames。" {
		t.Errorf("NotImplementedError = %q", msg)
	}
	pe := asPolicyError(fmt.Errorf("fetch: %w", netguard.ErrBlockedAddress))
	if pe == nil || pe.Error() == "" || !errors.Is(pe, netguard.ErrBlockedAddress) {
		t.Errorf("PolicyError = %+v", pe)
	}
	if asPolicyError(cause) != nil {
		t.Error("不是防護造成的錯誤不該轉成 PolicyError")
	}
	lib := &libraryError{err: cause}
	if lib.Error() != "查詢媒體庫失敗：boom" || !errors.Is(lib, cause) {
		t.Errorf("libraryError = %q", lib.Error())
	}
}

// AGT-R6：Agent 的錯誤帶著 HTTP 狀態、錯誤碼與是否已放回佇列；isLeaseExpired 只認 lease_expired（含被包裝的）。
func TestAgentError(t *testing.T) {
	e := &AgentError{Status: 409, Code: AgentCodeLeaseExpired, Reason: "lease", Message: "租約已過期", Returned: true}
	if e.Error() != "lease_expired: 租約已過期" {
		t.Errorf("Error = %q", e.Error())
	}
	if st, code, reason, msg, returned := e.AgentError(); st != 409 || code != AgentCodeLeaseExpired || reason != "lease" || msg != "租約已過期" || !returned {
		t.Errorf("AgentError() = %d %s %s %s %v", st, code, reason, msg, returned)
	}
	if !isLeaseExpired(fmt.Errorf("submit: %w", e)) {
		t.Error("包裝後仍是 lease_expired")
	}
	if isLeaseExpired(&AgentError{Code: AgentCodeNotFound}) || isLeaseExpired(errors.New("x")) || isLeaseExpired(nil) {
		t.Error("其他錯誤不是 lease_expired")
	}
}

func TestClockText(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "00:00:00", 59 * time.Second: "00:00:59", 3723 * time.Second: "01:02:03", 100*time.Hour + time.Minute: "100:01:00"} {
		if got := clockText(d); got != want {
			t.Errorf("clockText(%v) = %q，預期 %q", d, got, want)
		}
	}
}
