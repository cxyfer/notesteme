package api

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// fakeChannels 是記憶體內的頻道覆寫 service，行為依 D14：key 是 UUID 或 "<platform>:<external_id>"，
// 讀取 natural key 找不到不是錯誤，PATCH 找不到就建立，local 平台回 422。
type fakeChannels struct {
	byID      map[string]*pipeline.ChannelOverrides
	nextID    int
	lastPatch map[string]any
}

func newFakeChannels() *fakeChannels {
	return &fakeChannels{byID: map[string]*pipeline.ChannelOverrides{}}
}

func splitKey(key string) (platform, external string, ok bool) {
	return strings.Cut(key, ":")
}

func (f *fakeChannels) find(platform, external string) *pipeline.ChannelOverrides {
	for _, c := range f.byID {
		if c.Platform == platform && c.ExternalID == external {
			return c
		}
	}
	return nil
}

func (f *fakeChannels) ChannelOverrides(_ context.Context, key string) (pipeline.ChannelOverrides, error) {
	platform, external, natural := splitKey(key)
	if !natural {
		c, ok := f.byID[key]
		if !ok {
			return pipeline.ChannelOverrides{}, store.ErrNotFound
		}
		return *c, nil
	}
	if c := f.find(platform, external); c != nil {
		return *c, nil
	}
	return pipeline.ChannelOverrides{Platform: platform, ExternalID: external, Overrides: map[string]any{}}, nil
}

func (f *fakeChannels) PatchChannelOverrides(_ context.Context, key string, patch map[string]any) (pipeline.ChannelOverrides, error) {
	f.lastPatch = patch
	platform, external, natural := splitKey(key)
	var c *pipeline.ChannelOverrides
	if natural {
		switch platform {
		case "youtube", "bilibili", "podcast":
		default:
			return pipeline.ChannelOverrides{}, &pipeline.InvalidError{
				Msg: "id：這個平台沒有頻道。", Fields: []validate.FieldError{{Path: "id", Message: "這個平台沒有頻道。"}},
			}
		}
		if c = f.find(platform, external); c == nil {
			f.nextID++
			c = &pipeline.ChannelOverrides{ChannelID: "ch-" + string(rune('0'+f.nextID)), Platform: platform, ExternalID: external, Overrides: map[string]any{}}
			f.byID[c.ChannelID] = c
		}
	} else if c = f.byID[key]; c == nil {
		return pipeline.ChannelOverrides{}, store.ErrNotFound
	}
	c.Overrides = fakeMergePatch(c.Overrides, patch)
	return *c, nil
}

// fakeMergePatch 是 RFC 7396 的最小版本（真正的實作在 pipeline.MergePatch）。
func fakeMergePatch(target, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range target {
		out[k] = v
	}
	for k, v := range patch {
		switch pv := v.(type) {
		case nil:
			delete(out, k)
		case map[string]any:
			tm, _ := out[k].(map[string]any)
			out[k] = fakeMergePatch(tm, pv)
		default:
			out[k] = v
		}
	}
	return out
}

func TestChannelOverridesAPI_NaturalKeyCreatesChannel(t *testing.T) {
	r := newFakeRig(t)
	const key = "youtube:UCfixture000000000000000"

	// 還沒有頻道：GET 回 200，channel_id 空字串、overrides 是 {}（不是 404，場景在影音新增之前就會讀取）。
	rec := r.do("GET", "/api/channels/"+key+"/overrides", "", auth.ScopeRead)
	got := decodeMap(t, rec)
	if rec.Code != http.StatusOK || got["channel_id"] != "" || got["platform"] != "youtube" || got["external_id"] != "UCfixture000000000000000" {
		t.Fatalf("GET 尚未建立的頻道 = %d %s", rec.Code, rec.Body)
	}
	if ov, ok := got["overrides"].(map[string]any); !ok || len(ov) != 0 {
		t.Errorf("overrides = %v，預期 {}", got["overrides"])
	}

	// PATCH 建立頻道：200，有 channel_id。
	rec = r.do("PATCH", "/api/channels/"+key+"/overrides", `{"language":{"targets":["ja"]}}`, auth.ScopeAdmin)
	got = decodeMap(t, rec)
	if rec.Code != http.StatusOK || got["channel_id"] == "" || got["channel_id"] == nil {
		t.Fatalf("PATCH = %d %s，預期 200 並帶新頻道的 channel_id", rec.Code, rec.Body)
	}
	want := map[string]any{"language": map[string]any{"targets": []any{"ja"}}}
	if !reflect.DeepEqual(got["overrides"], want) {
		t.Errorf("overrides = %v，預期 %v", got["overrides"], want)
	}
	id := got["channel_id"].(string)

	// 同一個頻道可以用 UUID 讀取，內容相同；UUID 找不到是 404 not_found，訊息是頻道。
	rec = r.do("GET", "/api/channels/"+id+"/overrides", "", auth.ScopeRead)
	if g := decodeMap(t, rec); rec.Code != http.StatusOK || g["channel_id"] != id || !reflect.DeepEqual(g["overrides"], want) {
		t.Errorf("用 UUID 讀取 = %d %s", rec.Code, rec.Body)
	}
	rec = r.do("GET", "/api/channels/nope/overrides", "", auth.ScopeRead)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusNotFound || e.Code != CodeNotFound || !strings.Contains(e.Message, "頻道") {
		t.Errorf("UUID 找不到 = %d %s，預期 404 not_found 且訊息是頻道", rec.Code, rec.Body)
	}
	rec = r.do("PATCH", "/api/channels/nope/overrides", `{}`, auth.ScopeAdmin)
	if rec.Code != http.StatusNotFound {
		t.Errorf("PATCH UUID 找不到 = %d，預期 404", rec.Code)
	}
}

func TestChannelOverridesAPI_MergePatchNullDeletes(t *testing.T) {
	r := newFakeRig(t)
	const path = "/api/channels/youtube:UC1/overrides"
	r.do("PATCH", path, `{"language":{"targets":["ja"],"fallback":["groq"]},"timing":{"split_timing":"forbid"}}`, auth.ScopeAdmin)

	// null 原樣交給 service（Huma 沒有把它丟掉）；回應反映刪除之後的結果。
	rec := r.do("PATCH", path, `{"language":{"fallback":null},"timing":null}`, auth.ScopeAdmin)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body)
	}
	wantPatch := map[string]any{"language": map[string]any{"fallback": nil}, "timing": nil}
	if !reflect.DeepEqual(r.channels.lastPatch, wantPatch) {
		t.Errorf("傳給 service 的 patch = %#v，預期 %#v", r.channels.lastPatch, wantPatch)
	}
	want := map[string]any{"language": map[string]any{"targets": []any{"ja"}}}
	if got := decodeMap(t, rec)["overrides"]; !reflect.DeepEqual(got, want) {
		t.Errorf("overrides = %v，預期 %v", got, want)
	}

	// 整個 body 是 null 或不是物件：不進 service。
	r.channels.lastPatch = nil
	for _, body := range []string{`null`, `[]`, `"x"`} {
		rec = r.do("PATCH", path, body, auth.ScopeAdmin)
		if rec.Code < 400 || rec.Code >= 500 || rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Errorf("PATCH %s = %d %s，預期 4xx 的請求錯誤", body, rec.Code, rec.Body)
		}
	}
	if r.channels.lastPatch != nil {
		t.Errorf("不是物件的 body 不應呼叫 service：%v", r.channels.lastPatch)
	}
}

func TestChannelOverridesAPI_LocalRejected(t *testing.T) {
	r := newFakeRig(t)
	rec := r.do("PATCH", "/api/channels/local:whatever/overrides", `{"language":{"targets":["ja"]}}`, auth.ScopeAdmin)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != http.StatusUnprocessableEntity || e.Code != CodeInvalidInput || len(e.Errors) != 1 || e.Errors[0].Path != "id" {
		t.Errorf("local 平台 = %d %s，預期 422 invalid_request errors[0].path = id", rec.Code, rec.Body)
	}
	if len(r.channels.byID) != 0 {
		t.Errorf("local 平台不應建立頻道：%v", r.channels.byID)
	}
}
