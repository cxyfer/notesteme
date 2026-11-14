package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/events"
)

func countEvents(t *testing.T, s *Store) int64 {
	t.Helper()
	n, err := s.Read().CountEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// 交易中的狀態變更與事件一起 commit：事件的 type、item_id、payload 與發布給 SSE 的相同，時間取自注入的 clock。
func TestEventsPersistedInSameTransaction(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)

	var it Item
	err := s.Tx(ctx, func(r *Repo) error {
		var err error
		if it, _, err = r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123xxxxx", URL: "u"}); err != nil {
			return err
		}
		snap, err := r.EnsureSnapshot(ctx, "sha", `{}`)
		if err != nil {
			return err
		}
		_, err = r.InsertStageRun(ctx, NewStageRun{ItemID: it.ID, Stage: "metadata", Status: StatusPending, ConfigSnapshotID: snap})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, s); n != 2 {
		t.Fatalf("events 表有 %d 筆，預期 2（item.created 與 stage.status）", n)
	}

	rows, err := s.r.QueryContext(ctx, `SELECT type, item_id, payload_json, at FROM events ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []events.Event
	for rows.Next() {
		var typ, itemID, payload string
		var at int64
		if err := rows.Scan(&typ, &itemID, &payload, &at); err != nil {
			t.Fatal(err)
		}
		var ev events.Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("payload 不是事件的 JSON：%v", err)
		}
		if ev.Type != typ || itemID != it.ID || ev.ItemID != it.ID {
			t.Errorf("事件欄位不一致：type=%q item_id=%q payload=%+v", typ, itemID, ev)
		}
		if at != clk.Now().UnixMilli() {
			t.Errorf("事件時間 = %d，預期取自 clock %d", at, clk.Now().UnixMilli())
		}
		got = append(got, ev)
	}
	if len(got) != 2 || got[0].Type != events.TypeItemCreated || got[1].Type != events.TypeStageStatus || got[1].Stage != "metadata" || got[1].Status != StatusPending {
		t.Fatalf("事件 = %+v", got)
	}
}

// rollback 時事件跟著回復：不會留下沒有對應狀態變更的事件。
func TestEventsRolledBackWithTransaction(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	boom := errors.New("boom")
	err := s.Tx(ctx, func(r *Repo) error {
		if _, _, err := r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123xxxxx", URL: "u"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx = %v", err)
	}
	if n := countEvents(t, s); n != 0 {
		t.Fatalf("rollback 後 events 表有 %d 筆，預期 0", n)
	}
}

// 沒有設定 SSE 的 sink 時也要持久化（持久化不依賴有沒有訂閱者）；已存在的影音不產生事件。
func TestEventsPersistedWithoutSink(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	if _, created, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123xxxxx", URL: "u"}); err != nil || !created {
		t.Fatalf("created = %v, err = %v", created, err)
	}
	if _, created, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123xxxxx", URL: "u"}); err != nil || created {
		t.Fatalf("第二次 created = %v, err = %v", created, err)
	}
	if n := countEvents(t, s); n != 1 {
		t.Fatalf("events 表有 %d 筆，預期 1", n)
	}
}
