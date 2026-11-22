package cost

import (
	"slices"
	"testing"
)

func TestLookup(t *testing.T) {
	p, ok := Lookup("groq", "whisper-large-v3-turbo", UnitAudioHour)
	if !ok || p.USDPerUnit != 0.04 || p.Provider != "groq" || p.Unit != "audio_hour" {
		t.Fatalf("turbo = %+v, %v", p, ok)
	}
	if p, ok := Lookup("groq", "whisper-large-v3", "audio_hour"); !ok || p.USDPerUnit != 0.111 {
		t.Fatalf("v3 = %+v, %v", p, ok)
	}
	for _, c := range [][3]string{
		{"groq", "unknown-model", "audio_hour"},
		{"groq", "whisper-large-v3-turbo", "token"},
		{"other", "whisper-large-v3-turbo", "audio_hour"},
	} {
		if p, ok := Lookup(c[0], c[1], c[2]); ok {
			t.Errorf("Lookup(%v) 應該查不到，得到 %+v", c, p)
		}
	}
}

func TestPricesAsOfIsADate(t *testing.T) {
	if len(PricesAsOf) != len("2006-01-02") || PricesAsOf[4] != '-' || PricesAsOf[7] != '-' {
		t.Errorf("PricesAsOf = %q", PricesAsOf)
	}
}

func TestEstimateLLM(t *testing.T) {
	if _, ok := EstimateLLM("anthropic", "no-such-model", 1000, 1000); ok {
		t.Error("計價表沒有的 model 不能估算")
	}
	old := builtin
	defer func() { builtin = old }()
	builtin = append(slices.Clone(old),
		Price{Provider: "p", Model: "m", Unit: UnitInputMTok, USDPerUnit: 3},
		Price{Provider: "p", Model: "m", Unit: UnitOutputMTok, USDPerUnit: 15},
		Price{Provider: "p", Model: "half", Unit: UnitInputMTok, USDPerUnit: 3},
	)
	got, ok := EstimateLLM("p", "m", 2_000_000, 1_000_000)
	if !ok || got != 21 {
		t.Errorf("EstimateLLM = %v, %v，預期 21", got, ok)
	}
	if _, ok := EstimateLLM("p", "half", 10, 10); ok {
		t.Error("只有輸入單價時不能估算")
	}
}
