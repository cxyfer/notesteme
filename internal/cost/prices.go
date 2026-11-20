// Package cost 是內建計價表與花費估算用到的單價。
//
// 這裡的價格是內建預設值，跟著程式發布，不是即時價格：provider 調價後內容會過期，
// 要依各 provider 的公開價格定期確認，並更新 PricesAsOf。
// 使用者自訂的單價（price_overrides）屬於 M4 的 COST-03，之後在 Lookup 之前先查覆寫。
package cost

// PricesAsOf 是內建計價表最後一次對照 provider 公開價格的日期（YYYY-MM-DD）。
const PricesAsOf = "2026-11-21"

// UnitAudioHour 是以音訊長度（小時）計價的單位。
const UnitAudioHour = "audio_hour"

// Price 是一個 provider 的某個 model 在某種單位下的單價。
type Price struct {
	Provider   string
	Model      string
	Unit       string
	USDPerUnit float64
}

// builtin 是內建計價表。
var builtin = []Price{
	{Provider: "groq", Model: "whisper-large-v3-turbo", Unit: UnitAudioHour, USDPerUnit: 0.04},
	{Provider: "groq", Model: "whisper-large-v3", Unit: UnitAudioHour, USDPerUnit: 0.111},
}

// Lookup 查詢內建單價；表中沒有（未知的 model 或單位）時 ok 為 false。
func Lookup(provider, model, unit string) (Price, bool) {
	for _, p := range builtin {
		if p.Provider == provider && p.Model == model && p.Unit == unit {
			return p, true
		}
	}
	return Price{}, false
}
