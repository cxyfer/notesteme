package llm

import (
	"strings"
	"testing"
)

// 圖片區塊（FRM F4）：anthropic 是 base64 的 image block，openai 是 image_url 的 data URL；沒有圖片時 wire 不變。
func TestEncodeImages(t *testing.T) {
	req := testRequest()
	plainA, _ := (&HTTPClient{Type: TypeAnthropic}).encodeBody(t, req)
	plainO, _ := (&HTTPClient{Type: TypeOpenAI}).encodeBody(t, req)
	req.Images = []Image{{MediaType: "image/jpeg", Data: []byte{0xFF, 0xD8, 1}}}
	a, _ := (&HTTPClient{Type: TypeAnthropic}).encodeBody(t, req)
	o, _ := (&HTTPClient{Type: TypeOpenAI}).encodeBody(t, req)
	if !strings.Contains(a, `{"source":{"data":"/9gB","media_type":"image/jpeg","type":"base64"},"type":"image"}`) {
		t.Errorf("anthropic body = %s", a)
	}
	if !strings.Contains(o, `"url":"data:image/jpeg;base64,/9gB"`) || !strings.Contains(o, `"content":[{"text":`) {
		t.Errorf("openai body = %s", o)
	}
	if strings.Contains(plainA, `"image`) || strings.Contains(plainO, `"image`) || !strings.Contains(plainO, `{"content":"\u003cvideo_metadata`) {
		t.Errorf("沒有圖片時不應有圖片區塊：%s %s", plainA, plainO)
	}
}

func (c *HTTPClient) encodeBody(t *testing.T, req Request) (string, error) {
	t.Helper()
	_, body, err := c.encode(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), nil
}
