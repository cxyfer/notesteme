package events

import (
	"encoding/json"
	"testing"
)

// item.created 沒有步驟與狀態：JSON 不帶空的 stage、status；stage.status 兩者都在。
func TestEventJSON(t *testing.T) {
	tests := []struct {
		ev   Event
		want string
	}{
		{
			Event{Type: TypeItemCreated, Item: "yt:abc", ItemID: "id1"},
			`{"type":"item.created","item":"yt:abc","item_id":"id1"}`,
		},
		{
			Event{Type: TypeStageStatus, Item: "yt:abc", ItemID: "id1", Stage: "metadata", Status: "done"},
			`{"type":"stage.status","item":"yt:abc","item_id":"id1","stage":"metadata","status":"done"}`,
		},
	}
	for _, tt := range tests {
		b, err := json.Marshal(tt.ev)
		if err != nil || string(b) != tt.want {
			t.Errorf("Marshal(%+v) = %s, %v；預期 %s", tt.ev, b, err, tt.want)
		}
	}
}
