package youtube

import "testing"

func TestParseInfoMaxVideoFormat(t *testing.T) {
	info, err := parseInfo([]byte(`{"id":"abc","formats":[
		{"vcodec":"none","width":null,"height":null},
		{"vcodec":"avc1","width":1280,"height":720},
		{"vcodec":"vp9","width":3840,"height":2160},
		{"vcodec":"vp9","width":-1,"height":99999}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.MaxVideoW != 3840 || info.MaxVideoH != 2160 {
		t.Errorf("max = %dx%d", info.MaxVideoW, info.MaxVideoH)
	}
	info, _ = parseInfo([]byte(`{"id":"abc"}`))
	if info.MaxVideoW != 0 {
		t.Error("沒有 formats 時應為 0")
	}
}
