package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
)

// SPB-R4：步驟沒有執行過時沒有 sponsor_segments；沒有片段時是空陣列；時間是 HH:MM:SS.mmm。
func TestDetailSponsorSegments(t *testing.T) {
	enc := func(d pipeline.Detail) string {
		b, err := json.Marshal(toDetail(d))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := enc(pipeline.Detail{}); strings.Contains(got, "sponsor_segments") {
		t.Errorf("沒有執行過不該有欄位：%s", got)
	}
	if got := enc(pipeline.Detail{SponsorSegments: []sponsorblock.Segment{}}); !strings.Contains(got, `"sponsor_segments":[]`) {
		t.Errorf("沒有片段要是空陣列：%s", got)
	}
	got := enc(pipeline.Detail{SponsorSegments: []sponsorblock.Segment{{Category: "sponsor", StartMS: 60000, EndMS: 3723456}}})
	if !strings.Contains(got, `"sponsor_segments":[{"category":"sponsor","start":"00:01:00.000","end":"01:02:03.456"}]`) {
		t.Errorf("片段：%s", got)
	}
}
