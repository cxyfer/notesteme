package subtitle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// CuesReader 逐句解碼 notesteme cues JSON（ADR-0005），記憶體用量不隨句數成長（NFR-RES-3）。
// 檢查與 DecodeCuesJSON 相同：大小上限、變體、時間來源與時間範圍。
//
// lang、variant、speakers 必須出現在 cues 之前（RenderCuesJSON 寫出的順序）：NewCuesReader 讀到 cues 陣列的開頭
// 才回傳，這時 Header 已經完整；cues 之後的欄位照樣解碼，但不會再改變已經回傳的 Header。
type CuesReader struct {
	dec    *json.Decoder
	Header Transcript
	n      int
	done   bool
}

// errNoCues 表示 cues JSON 沒有 cues 陣列。
var errNoCues = errors.New("subtitle: decode cues json: 沒有 cues")

// NewCuesReader 讀到 cues 陣列的開頭並檢查 Header 的變體。
func NewCuesReader(r io.Reader) (*CuesReader, error) {
	cr := &CuesReader{dec: json.NewDecoder(io.LimitReader(r, maxCuesJSONBytes))}
	if err := cr.expectDelim('{'); err != nil {
		return nil, err
	}
	for {
		key, end, err := cr.nextKey()
		if err != nil {
			return nil, err
		}
		if end {
			return nil, errNoCues
		}
		if key == "cues" {
			break
		}
		if err := cr.decodeHeaderField(key); err != nil {
			return nil, err
		}
	}
	if !cr.Header.Variant.Valid() {
		return nil, fmt.Errorf("subtitle: unknown variant %q", cr.Header.Variant)
	}
	tok, err := cr.dec.Token()
	if err != nil {
		return nil, decodeErr(err)
	}
	switch tok {
	case json.Delim('['):
	case nil: // "cues": null 與 DecodeCuesJSON 一樣視為沒有句子
		cr.done = true
		return cr, cr.finish()
	default:
		return nil, decodeErr(fmt.Errorf("cues 不是陣列"))
	}
	return cr, nil
}

// Next 回傳下一句；沒有更多句子時 ok 為 false。
func (cr *CuesReader) Next() (c Cue, ok bool, err error) {
	if cr.done {
		return Cue{}, false, nil
	}
	if !cr.dec.More() {
		cr.done = true
		if _, err := cr.dec.Token(); err != nil { // ]
			return Cue{}, false, decodeErr(err)
		}
		return Cue{}, false, cr.finish()
	}
	if err := cr.dec.Decode(&c); err != nil {
		return Cue{}, false, decodeErr(err)
	}
	cr.n++
	if c.Timing != "" && !c.Timing.Valid() {
		return Cue{}, false, fmt.Errorf("subtitle: cue %d: unknown timing %q", cr.n, c.Timing)
	}
	if c.StartMS < 0 || c.EndMS < c.StartMS {
		return Cue{}, false, fmt.Errorf("subtitle: cue %d: invalid time range", cr.n)
	}
	return c, true, nil
}

// finish 讀完 cues 之後的欄位與結尾的 }。
func (cr *CuesReader) finish() error {
	for {
		key, end, err := cr.nextKey()
		if err != nil {
			return err
		}
		if end {
			return nil
		}
		var skip json.RawMessage
		if key == "cues" {
			return decodeErr(fmt.Errorf("cues 重複"))
		}
		if err := cr.dec.Decode(&skip); err != nil {
			return decodeErr(err)
		}
	}
}

func (cr *CuesReader) decodeHeaderField(key string) error {
	var err error
	switch key {
	case "lang":
		err = cr.dec.Decode(&cr.Header.Lang)
	case "variant":
		err = cr.dec.Decode(&cr.Header.Variant)
	case "speakers":
		err = cr.dec.Decode(&cr.Header.Speakers)
	default:
		var skip json.RawMessage
		err = cr.dec.Decode(&skip)
	}
	return decodeErr(err)
}

// nextKey 讀下一個物件鍵；讀到 } 時 end 為 true。
func (cr *CuesReader) nextKey() (key string, end bool, err error) {
	tok, err := cr.dec.Token()
	if err != nil {
		return "", false, decodeErr(err)
	}
	if tok == json.Delim('}') {
		return "", true, nil
	}
	k, ok := tok.(string)
	if !ok {
		return "", false, decodeErr(fmt.Errorf("預期物件鍵，得到 %v", tok))
	}
	return k, false, nil
}

func (cr *CuesReader) expectDelim(d json.Delim) error {
	tok, err := cr.dec.Token()
	if err != nil {
		return decodeErr(err)
	}
	if tok != d {
		return decodeErr(fmt.Errorf("預期 %v，得到 %v", d, tok))
	}
	return nil
}

func decodeErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("subtitle: decode cues json: %w", err)
}
