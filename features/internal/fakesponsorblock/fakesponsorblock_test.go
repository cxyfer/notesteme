package fakesponsorblock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, []apiVideo) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []apiVideo
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return resp, out
}

func TestHashPrefixLookup(t *testing.T) {
	fake := New()
	fake.SetSegments("spon01xxxxx", []Segment{
		{Category: "intro", Start: 0, End: 10},
		{Category: "sponsor", Start: 60, End: 90},
		{Category: "sponsor", Action: "mute", Start: 100, End: 110},
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	q := "?categories=" + url.QueryEscape(`["sponsor","selfpromo"]`) + "&actionTypes=" + url.QueryEscape(`["skip"]`)
	resp, out := get(t, srv, "/api/skipSegments/"+HashPrefix("spon01xxxxx", 4)+q)
	if resp.StatusCode != http.StatusOK || len(out) != 1 || out[0].VideoID != "spon01xxxxx" {
		t.Fatalf("status %d, out %+v", resp.StatusCode, out)
	}
	if got := out[0].Segments; len(got) != 1 || got[0].Category != "sponsor" || got[0].Segment != [2]float64{60, 90} {
		t.Errorf("segments = %+v（intro 不在類別內、mute 不是 skip）", got)
	}
	if fake.SawVideoID("spon01xxxxx") {
		t.Error("hash 前綴的查詢不應該被當成收到影片 ID")
	}
	if n := fake.RequestsFor("spon01xxxxx"); n != 1 {
		t.Errorf("RequestsFor = %d", n)
	}
}

func TestNoSegmentsIs404AndFailOverrides(t *testing.T) {
	fake := New()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if resp, _ := get(t, srv, "/api/skipSegments/"+HashPrefix("none0000000", 4)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("沒有片段 = %d", resp.StatusCode)
	}
	fake.Fail("spon01xxxxx", http.StatusServiceUnavailable)
	if resp, _ := get(t, srv, "/api/skipSegments/"+HashPrefix("spon01xxxxx", 4)); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Fail = %d", resp.StatusCode)
	}
	if resp, _ := get(t, srv, "/api/skipSegments/zz"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("不合法的前綴 = %d", resp.StatusCode)
	}
}

func TestLegacyVideoIDLookupIsRecorded(t *testing.T) {
	fake := New()
	fake.SetSegments("spon01xxxxx", []Segment{{Category: "sponsor", Start: 1, End: 2}})
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if resp, out := get(t, srv, "/api/skipSegments?videoID=spon01xxxxx"); resp.StatusCode != http.StatusOK || len(out) != 1 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !fake.SawVideoID("spon01xxxxx") {
		t.Error("直接帶影片 ID 的請求要被記錄")
	}
}
