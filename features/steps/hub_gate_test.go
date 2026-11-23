package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

// 真 Hub 的閘門只對已實作範圍的 HACC 場景放行（M5-S1：R1～R8；M5-S2：R9～R13、R18），
// 混了其他規則、其他領域，或用到 Hub 內容句型（steps.md 4.31）的場景維持 pending，而且是裸的 ErrPending（junit 的訊息與切片之前相同）。
func TestHubGate(t *testing.T) {
	content := []string{"the Hub has the version \"raw\" of \"yt:hacc01xxxxx\" with:", "the Hub client sends \"GET /api/v1/versions\""}
	plain := []string{"a Hub is running", "the hub member \"root\" signed in with \"github\""}
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
		{[]string{"@M5", "@HUB-07", "@HACC-R13"}, content, true},            // 要先建立版本（HREP）
		{[]string{"@M5", "@HUB-07", "@HACC-R13", "@HACC-R14"}, plain, true}, // 可見度是 S3 以後
		{[]string{"@M5", "@HUB-07", "@HACC-R15"}, plain, true},              // 下架是 S5
		{[]string{"@M5", "@HUB-01", "@HACC-R6", "@HACC-R17"}, plain, true},  // 混了 S5 的規則
		{[]string{"@M5", "@HUB-06", "@HREP-R13", "@HACC-R17"}, plain, true}, // 其他領域
		{[]string{"@M5", "@HUB-06", "@HREP-R13", "@HACC-R9"}, plain, true},  // 其他領域混了已實作的規則
		{[]string{"@M5", "@HUB-02", "@HREP-R1"}, plain, true},
		{[]string{"@M5", "@HUB-05", "@HMCP-R1"}, plain, true},
		{[]string{"@M5", "@HUB-07", "@HACC-R9"}, []string{"the hub member \"alice\" shares the bundle \"b\""}, true},
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
