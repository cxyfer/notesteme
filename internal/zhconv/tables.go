// Package zhconv 是 OpenCC 的純 Go 實作：依內嵌的上游設定（config/*.json）與字典，
// 做 normalization、mmseg 最大正向匹配分段與 conversion_chain 轉換（group 的 short_circuit 與 union）。
// 不依賴任何第三方套件；字典是 OpenCC 官方的 Apache-2.0 資源（見 opencc/NOTICE.md 與 ADR-0018）。
package zhconv

import (
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/langtag"
)

// Table 是 OpenCC 的轉換設定名稱（對應上游的 config/<table>.json）。
type Table string

const (
	S2T   Table = "s2t"
	S2TW  Table = "s2tw"
	S2TWP Table = "s2twp"
	S2HK  Table = "s2hk"
	T2S   Table = "t2s"
	TW2S  Table = "tw2s"
	TW2SP Table = "tw2sp"
	HK2S  Table = "hk2s"
	T2TW  Table = "t2tw"
	T2HK  Table = "t2hk"

	// HK2TW、TW2HK 是繁體地區之間的轉換：上游沒有對應的設定。t2tw、t2hk 的輸入是 OpenCC Standard，
	// 直接套用會漏掉來源地區特有的字形（香港「紅綫」、台灣「屋裡」），所以先用來源地區的反向字典
	// 轉成 Standard，再套用目標地區的字典（Codex P2）。由 composite 在程式裡組出，沒有 json 檔。
	HK2TW Table = "hk2tw"
	TW2HK Table = "tw2hk"
)

var allTables = []Table{S2T, S2TW, S2TWP, S2HK, T2S, TW2S, TW2SP, HK2S, T2TW, T2HK}

// compositeTables 是本實作自行組合的轉換表（見 HK2TW）。
var compositeTables = []Table{HK2TW, TW2HK}

// Tables 回傳內嵌的轉換表（上游設定十份，加上自行組合的兩份）。
func Tables() []Table { return append(append([]Table(nil), allTables...), compositeTables...) }

// DataVersion 是內嵌字典的 OpenCC 版本；放進 translate 的快取鍵與來源紀錄，字典更新後舊快取不再命中。
// 必須與 opencc/VERSION 一致（TestDataVersionMatchesFile 檢查）。
const DataVersion = "ver.1.4.2"

// Strategy 對應 language.variant_strategy。
type Strategy string

const (
	StrategyConvert  Strategy = "convert"
	StrategyLocalize Strategy = "localize"
)

// Valid 回報 t 是不是內嵌的轉換表。
func Valid(t Table) bool {
	return slices.Contains(allTables, t) || slices.Contains(compositeTables, t)
}

// TableFor 回傳 source → target 的轉換表；兩者不都是中文、字形無法判斷、或不需要轉換（同一字形且沒有對應的地區表）時 ok 為 false。
// source 沒有 script、也沒有可推定的 region 時（例如 "zh"），呼叫端先用 DetectScript 補成 zh-Hans 或 zh-Hant。
//
// convert 與 localize 只有台灣目標的簡體來源不同：convert 用 s2twp（含台灣用詞），localize 的前置轉換用 s2tw（只轉字形，
// 用詞交給 LLM，TRN-R9）；繁體轉簡體時 convert 用 tw2sp（含中國大陸用詞）、localize 用 tw2s。
func TableFor(source, target string, s Strategy) (t Table, ok bool) {
	ss, ts := langtag.ChineseScript(source), langtag.ChineseScript(target)
	if ss == "" || ts == "" {
		return "", false
	}
	sr, tr := langtag.Parse(source).Region, langtag.Parse(target).Region
	hk := func(r string) bool { return r == "HK" || r == "MO" }
	localize := s == StrategyLocalize
	switch {
	case ss == "Hans" && ts == "Hant":
		switch {
		case tr == "TW" && localize:
			return S2TW, true
		case tr == "TW":
			return S2TWP, true
		case hk(tr):
			return S2HK, true
		}
		return S2T, true
	case ss == "Hant" && ts == "Hans":
		switch {
		case sr == "TW" && localize:
			return TW2S, true
		case sr == "TW":
			return TW2SP, true
		case hk(sr):
			return HK2S, true
		}
		return T2S, true
	case ss == "Hant" && ts == "Hant":
		switch {
		case tr == "TW" && hk(sr):
			return HK2TW, true
		case tr == "TW" && sr != "TW":
			return T2TW, true
		case hk(tr) && sr == "TW":
			return TW2HK, true
		case hk(tr) && !hk(sr):
			return T2HK, true
		}
	}
	return "", false
}

// GlossaryTable 回傳把詞彙表轉成原文字形的轉換表（COR-R4）：原文是簡體用 tw2sp（含中國大陸用詞，資料庫 → 数据库），
// 台灣繁體用 s2twp，香港與澳門繁體用 s2hk，其他繁體用 s2t。原文不是中文或字形無法判斷時 ok 為 false。
func GlossaryTable(source string) (Table, bool) {
	t := langtag.Parse(source)
	switch langtag.ChineseScript(source) {
	case "Hans":
		return TW2SP, true
	case "Hant":
		switch t.Region {
		case "TW":
			return S2TWP, true
		case "HK", "MO":
			return S2HK, true
		}
		return S2T, true
	}
	return "", false
}

// versionFile 讀取內嵌的 VERSION（測試用）。
func versionFile() string {
	b, err := resources.ReadFile(resourceDir + "VERSION")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
