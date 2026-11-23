package cost

import (
	"math"
	"strconv"
	"strings"
)

// Micro 是整數 micro-USD（1e-6 美元）。加總、比較與預算檢查一律用它，不直接加總浮點數（COST 規則表上方、C10）。
type Micro int64

// MaxUSD 是可以換成 Micro 的最大金額；更大的數字（或 NaN、無限大、負數）不是合理的金額。
const MaxUSD = 1e9

// FromUSD 把美元四捨五入成 micro-USD；負數、NaN、無限大或超過 MaxUSD 時 ok 為 false。
func FromUSD(v float64) (Micro, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > MaxUSD {
		return 0, false
	}
	return Micro(math.Round(v * 1e6)), true
}

// USD 是 API 的 JSON 中的金額：micro-USD 除以 1e6（最多 6 位小數）。
// 整數除法的結果是最接近十進位值的 float64，所以 0.30 會是 0.3 而不是 0.30000000000000004。
func (m Micro) USD() float64 { return float64(m) / 1e6 }

// Dollars 是給使用者看的金額：四捨五入到小數兩位，前面加 $，例如 $0.04、$20.00。
func (m Micro) Dollars() string {
	neg := m < 0
	if neg {
		m = -m
	}
	cents := (int64(m) + 5000) / 10000
	s := "$" + strconv.FormatInt(cents/100, 10) + "." + pad2(cents%100)
	if neg {
		s = "-" + s
	}
	return s
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

// CSV 是 CSV 的 usd 欄（COST-R15）：不帶 $，至少 2 位、最多 6 位小數，多餘的 0 去掉，例如 0.10、0.0123。
func (m Micro) CSV() string {
	neg := m < 0
	if neg {
		m = -m
	}
	whole, frac := int64(m)/1e6, int64(m)%1e6
	f := strings.TrimRight(strconv.FormatInt(1e6+frac, 10)[1:], "0")
	for len(f) < 2 {
		f += "0"
	}
	s := strconv.FormatInt(whole, 10) + "." + f
	if neg {
		s = "-" + s
	}
	return s
}

// llmMicro 是一次 LLM 呼叫的金額：輸入與輸出 token 各乘以每百萬 token 的單價（每百萬 token 1 美元 = 每個 token
// 1 micro-USD），合計後四捨五入一次。負數的 token 數或單價當作 0。
func llmMicro(in, out int, inPerMTok, outPerMTok float64) Micro {
	v := float64(max(in, 0))*max(inPerMTok, 0) + float64(max(out, 0))*max(outPerMTok, 0)
	if math.IsNaN(v) || math.IsInf(v, 0) || v > MaxUSD*1e6 {
		return Micro(MaxUSD * 1e6)
	}
	return Micro(math.Round(v))
}
