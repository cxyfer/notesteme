package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

// 真 Hub 的閘門只對已實作範圍的場景放行（M5-S1：HACC-R1～R8；M5-S2：R9～R13、R18；M5-S3：HACC-R14 與 HREP-R1～R6、R8～R12；M5-S4：HREP-R7、R16～R19；M5-S5：HACC-R15～R17、HREP-R13～R15），
// 混了其他規則或其他領域的場景維持 pending，而且是裸的 ErrPending（junit 的訊息與切片之前相同）。
// M5-S3 起 Hub 內容的句型（steps.md 4.31）已實作，不再擋場景。
func TestHubGate(t *testing.T) {
	content := []string{"the Hub has the version \"raw\" of \"yt:hacc01xxxxx\" with:", "the Hub client sends \"GET /api/v1/versions\""}
	plain := []string{"a Hub is running", "the hub member \"root\" signed in with \"github\""}
	share := []string{"the hub member \"alice\" shares the bundle \"b\""}
	cases := []struct {
		tags    []string
		steps   []string
		pending bool
	}{
		{[]string{"@M5", "@HUB-01", "@HACC-R1"}, plain, false},
		{[]string{"@M5", "@HUB-01", "@HACC-R2", "@HACC-R3"}, plain, false},
		{[]string{"@M5", "@HUB-01", "@HACC-R4", "@HACC-R10"}, plain, false},
		{[]string{"@M5", "@HUB-01", "@HACC-R8"}, plain, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R9"}, plain, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R9", "@HACC-R11"}, plain, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R10", "@HACC-R11"}, plain, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R12", "@HACC-R13"}, plain, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R18"}, plain, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R13"}, content, false},              // M5-S3：內容句型已實作
		{[]string{"@M5", "@HUB-07", "@HACC-R13", "@HACC-R14"}, content, false}, // M5-S3：可見度
		{[]string{"@M5", "@HUB-07", "@HACC-R14"}, plain, false},
		{[]string{"@M5", "@HUB-07", "@HACC-R9"}, share, false},
		{[]string{"@M5", "@HUB-02", "@HREP-R1"}, share, false},
		{[]string{"@M5", "@HUB-02", "@HREP-R2"}, share, false},
		{[]string{"@M5", "@HUB-02", "@HREP-R6"}, content, false},
		{[]string{"@M5", "@HUB-02", "@HUB-04", "@HREP-R6"}, content, false},
		{[]string{"@M5", "@HUB-02", "@HREP-R12"}, share, false},
		{[]string{"@M5", "@HUB-03", "@HREP-R7"}, share, false},                 // M5-S4：以列表驗證
		{[]string{"@M5", "@HUB-03", "@HREP-R16"}, content, false},              // M5-S4：篩選
		{[]string{"@M5", "@HUB-03", "@HREP-R17"}, content, false},              // M5-S4：詳情與影片頁
		{[]string{"@M5", "@HUB-03", "@HREP-R18"}, content, false},              // M5-S4：下載
		{[]string{"@M5", "@HUB-05", "@HREP-R19"}, content, false},              // M5-S4：lookup
		{[]string{"@M5", "@HUB-06", "@HREP-R13"}, content, false},              // M5-S5
		{[]string{"@M5", "@HUB-06", "@HREP-R14"}, content, false},              // M5-S5
		{[]string{"@M5", "@HUB-06", "@HREP-R15"}, content, false},              // M5-S5
		{[]string{"@M5", "@HUB-03", "@HREP-R16", "@HREP-R13"}, content, false}, // M5-S5
		{[]string{"@M5", "@HUB-07", "@HACC-R15"}, plain, false},                // M5-S5
		{[]string{"@M5", "@HUB-07", "@HACC-R16"}, content, false},              // M5-S5
		{[]string{"@M5", "@HUB-01", "@HACC-R6", "@HACC-R17"}, plain, false},    // M5-S5
		{[]string{"@M5", "@HUB-06", "@HREP-R13", "@HACC-R17"}, plain, false},   // M5-S5
		{[]string{"@M5", "@HUB-06", "@HREP-R13", "@HACC-R9"}, plain, false},    // M5-S5
		{[]string{"@M5", "@HUB-02", "@HREP-R1", "@HACC-R15"}, share, false},    // M5-S5
		{[]string{"@M5", "@HUB-07", "@HACC-R19"}, plain, true},                 // 規格沒有的規則
		{[]string{"@M5", "@HUB-06", "@HREP-R20"}, content, true},               // 規格沒有的規則
		{[]string{"@M5", "@HUB-06", "@HREP-R13", "@HACC-R19"}, plain, true},    // 混了不認得的規則
		{[]string{"@M5", "@HUB-08", "@HMCP-R1", "@HACC-R17"}, plain, true},     // HMCP 不是 S5
		{[]string{"@M5", "@HUB-05", "@HMCP-R1"}, plain, true},
		{[]string{"@M5", "@HUB-02", "@HREP-R1", "@HSHR-R1"}, share, true},
		{[]string{"@M5", "@HUB-02", "@HREP-Rx"}, share, true},
		{[]string{"@M5", "@HUB-02"}, share, true},
		{nil, plain, true},
	}
	for _, c := range cases {
		s := &Scenario{tags: c.tags, stepTexts: c.steps}
		err := s.hubGate()
		if errors.Is(err, godog.ErrPending) != c.pending {
			t.Errorf("%v：err = %v，預期 pending=%v", c.tags, err, c.pending)
		}
		if c.pending && err != godog.ErrPending {
			t.Errorf("%v：要回傳裸的 godog.ErrPending，實際 %v", c.tags, err)
		}
	}
}
