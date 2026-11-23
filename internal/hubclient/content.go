package hubclient

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// 字幕內容的上限（與匯入字幕的驗證器同一組規則，OUT-R13～R15）。
const (
	maxCues      = 100000
	maxCueRunes  = 2000
	prefixLength = 4
)

// SHA256Hex 回傳 data 的 sha256（小寫十六進位）。
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// VideoKey 是 ASR 前查詢的鍵 "<platform>:<external_id>:<part>"，沒有分P時 part 是空字串（ADR-0032 第 3 點）。
func VideoKey(platform, externalID string, part int) string {
	p := ""
	if part > 0 {
		p = strconv.Itoa(part)
	}
	return platform + ":" + externalID + ":" + p
}

// VideoKeySHA256 回傳 VideoKey 的 sha256。
func VideoKeySHA256(platform, externalID string, part int) string {
	return SHA256Hex([]byte(VideoKey(platform, externalID, part)))
}

// Prefix 回傳查詢送出的前綴（sha256 的前 4 個小寫十六進位字元）。
func Prefix(keySHA string) string { return keySHA[:prefixLength] }

// ValidateCues 檢查 Hub 回來的 cues JSON（HSHR-R10）：可以解碼、至少一句、每句結束不早於開始、
// 開始時間不比前一句早、文字是合法 UTF-8 且不超過上限。錯誤訊息是給使用者看的原因。
func ValidateCues(data []byte) (subtitle.Transcript, error) {
	if !utf8.Valid(data) {
		return subtitle.Transcript{}, contentInvalid("不是 UTF-8")
	}
	tr, err := subtitle.DecodeCuesJSON(bytes.NewReader(data))
	if err != nil {
		if strings.Contains(err.Error(), "invalid time range") {
			return subtitle.Transcript{}, contentInvalid("時間範圍不正確")
		}
		return subtitle.Transcript{}, contentInvalid("無法解析 cues JSON")
	}
	if len(tr.Cues) == 0 {
		return subtitle.Transcript{}, contentInvalid("沒有任何字幕句")
	}
	if len(tr.Cues) > maxCues {
		return subtitle.Transcript{}, contentInvalid(fmt.Sprintf("字幕句超過 %d 句", maxCues))
	}
	if tr.Lang != "" && !langRe.MatchString(tr.Lang) {
		return subtitle.Transcript{}, contentInvalid("語言標記不正確")
	}
	var prev int64
	out := make([]subtitle.Cue, 0, len(tr.Cues))
	for i, c := range tr.Cues {
		if c.EndMS < c.StartMS {
			return subtitle.Transcript{}, contentInvalid(fmt.Sprintf("第 %d 句的結束時間早於開始時間", i+1))
		}
		if c.StartMS < prev {
			return subtitle.Transcript{}, contentInvalid(fmt.Sprintf("第 %d 句的開始時間早於前一句", i+1))
		}
		prev = c.StartMS
		if utf8.RuneCountInString(c.Text) > maxCueRunes {
			return subtitle.Transcript{}, contentInvalid(fmt.Sprintf("第 %d 句太長", i+1))
		}
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		c.Index = len(out) + 1
		c.Timing = subtitle.TimingSource
		c.Speaker = nil
		out = append(out, c)
	}
	if len(out) == 0 {
		return subtitle.Transcript{}, contentInvalid("沒有任何字幕句")
	}
	tr.Cues, tr.Speakers = out, nil
	return tr, nil
}

func contentInvalid(detail string) *Error { return &Error{Kind: KindContentInvalid, Detail: detail} }

// Pick 依 HSHR-R14 從查詢結果挑出一個版本：影片雜湊必須完全相同；語言與 lang 相同（lang 為空或 und 時不限）；
// 依序偏好 source、model_verified、score 高、sha256 字典序小。沒有候選時 ok 為 false。
func Pick(videos []LookupVideo, keySHA, lang string) (LookupVersion, bool) {
	want := transcript.PrimaryLang(lang)
	if want == "und" {
		want = ""
	}
	var cands []LookupVersion
	for _, v := range videos {
		if v.VideoKeySHA256 != keySHA {
			continue
		}
		for _, x := range v.Versions {
			if want != "" && transcript.PrimaryLang(x.Lang) != want {
				continue
			}
			cands = append(cands, x)
		}
	}
	if len(cands) == 0 {
		return LookupVersion{}, false
	}
	slices.SortFunc(cands, func(a, b LookupVersion) int {
		return cmp.Or(
			boolRank(a.Variant == "source", b.Variant == "source"),
			boolRank(a.ModelVerified, b.ModelVerified),
			cmp.Compare(b.Score, a.Score),
			strings.Compare(a.SHA256, b.SHA256),
		)
	})
	return cands[0], true
}

// boolRank 讓 true 排在前面。
func boolRank(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}
	return 1
}
