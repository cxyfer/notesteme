package fakellm

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestCheckImageBlocks(t *testing.T) {
	jpg := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xE0})
	ok := map[string]string{
		providerAnthropic: `[{"type":"text","text":"x"},{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"` + jpg + `"}}]`,
		providerOpenAI:    `[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,` + jpg + `"}}]`,
	}
	for id, c := range ok {
		if errs := checkImageBlocks(id, json.RawMessage(c)); len(errs) != 0 {
			t.Errorf("%s：%v", id, errs)
		}
	}
	bad := map[string]string{
		"png":        `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + jpg + `"}}]`,
		"not jpeg":   `[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"aGVsbG8="}}]`,
		"url source": `[{"type":"image","source":{"type":"url","media_type":"image/jpeg"}}]`,
		"openai blk": `[{"type":"image_url","image_url":{"url":"https://x/y.jpg"}}]`,
	}
	for name, c := range bad {
		if errs := checkImageBlocks(providerAnthropic, json.RawMessage(c)); len(errs) == 0 {
			t.Errorf("%s 應有違規", name)
		}
	}
	if errs := checkImageBlocks(providerAnthropic, json.RawMessage(`"plain"`)); len(errs) != 0 {
		t.Error("字串 content 不檢查")
	}
}
