package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 寫回結果要帶領取時拿到的 lease_token：token 不對、任務已被放回或已結束時，寫回無效（回傳 ErrLeaseLost）。
func TestTaskWritesRequireTheCurrentLease(t *testing.T) {
	ctx := context.Background()
	writes := map[string]func(r *Repo, id, token string) error{
		"complete": func(r *Repo, id, token string) error { return r.CompleteTask(ctx, id, token) },
		"fail":     func(r *Repo, id, token string) error { return r.FailTask(ctx, id, token) },
		"requeue":  func(r *Repo, id, token string) error { return r.RequeueTask(ctx, id, token, nil, true) },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			s, _ := openTest(t)
			_, sr := seedItem(t, s, "a")
			tk := newTask(t, s, sr, 3, nil)
			held := claim(t, s, claimOpts("w", 1))[0]

			// 別人的 token：任務維持 leased。
			if err := write(s.Write(), tk.ID, "someone-elses-token"); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("token 不符應回傳 ErrLeaseLost，實際 %v", err)
			}
			if got, _ := s.Read().Task(ctx, tk.ID); got.State != TaskLeased || got.LeaseToken != held.LeaseToken {
				t.Fatalf("token 不符不應改動任務：%+v", got)
			}

			// 租約被放回（重啟）之後，舊的持有者晚到的寫回無效，任務不會被蓋回 done。
			if _, err := s.Write().ReleaseLeases(ctx, "new-worker"); err != nil {
				t.Fatal(err)
			}
			if err := write(s.Write(), tk.ID, held.LeaseToken); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("租約放回後應回傳 ErrLeaseLost，實際 %v", err)
			}
			if got, _ := s.Read().Task(ctx, tk.ID); got.State != TaskPending {
				t.Fatalf("放回後的任務應維持 pending：%+v", got)
			}

			// 重新領取後，新的 token 才有效；結束後同一個 token 不能再寫一次。
			again := claim(t, s, claimOpts("new-worker", 1))[0]
			if again.LeaseToken == held.LeaseToken {
				t.Fatal("重新領取應換新的 token")
			}
			if err := write(s.Write(), tk.ID, again.LeaseToken); err != nil {
				t.Fatalf("目前的租約應能寫回：%v", err)
			}
			if name != "requeue" {
				if err := write(s.Write(), tk.ID, again.LeaseToken); !errors.Is(err, ErrLeaseLost) {
					t.Fatalf("任務結束後不能再寫回：%v", err)
				}
			}
		})
	}
}

// 佇列頁的 pending 清單與 ClaimTasks 用同一個排序：第一筆 pending 就是領取一個（max=1）會拿到的任務。
func TestQueueTasksFirstPendingIsWhatClaimTakes(t *testing.T) {
	ctx := context.Background()
	aging := 10 * time.Minute
	type seeded struct {
		name string
		prio int
		wait time.Duration // 建立後再等多久才建立下一個任務
		nb   time.Duration // not_before 距離建立時間（0 代表沒有）
	}
	tests := []struct {
		name  string
		tasks []seeded
		// 逐次領取（max=1）的預期順序，以 name 表示。
		want []string
	}{
		{"手動先於回補", []seeded{{"backfill", 1, 0, 0}, {"manual", 3, 0, 0}}, []string{"manual", "backfill"}},
		{"同級先建立的先", []seeded{{"first", 3, 0, 0}, {"second", 3, 0, 0}}, []string{"first", "second"}},
		{"回補等了三小時就排在新的手動前面", []seeded{{"old", 1, 3 * time.Hour, 0}, {"new", 3, 0, 0}}, []string{"old", "new"}},
		{"回補只差兩級：等 20 分鐘就追平，先建立的先", []seeded{{"old", 1, 20 * time.Minute, 0}, {"new", 3, 0, 0}}, []string{"old", "new"}},
		{"not_before 未到的排在後面", []seeded{{"later", 3, 0, time.Hour}, {"now", 1, 0, 0}}, []string{"now"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, clk := openTest(t)
			names := map[string]string{} // task id -> name
			for i, sd := range tt.tasks {
				_, sr := seedItem(t, s, string(rune('a'+i)))
				var nb *time.Time
				if sd.nb > 0 {
					at := clk.Now().Add(sd.nb)
					nb = &at
				}
				names[newTask(t, s, sr, sd.prio, nb).ID] = sd.name
				clk.Advance(sd.wait)
			}

			for _, want := range tt.want {
				list, err := s.Read().QueueTasks(ctx, "builtin", aging)
				if err != nil {
					t.Fatal(err)
				}
				var first string
				for _, e := range list {
					if e.State == TaskPending {
						first = e.ID
						break
					}
				}
				got := claim(t, s, ClaimOptions{Owner: "w", Max: 1, Lease: time.Minute, Executor: "builtin", AgingInterval: aging})
				if len(got) != 1 {
					t.Fatalf("應領取到 %q，實際沒有任務", want)
				}
				if got[0].ID != first {
					t.Fatalf("pending 清單第一筆是 %q，領取到的是 %q", names[first], names[got[0].ID])
				}
				if names[got[0].ID] != want {
					t.Fatalf("領取到 %q，預期 %q", names[got[0].ID], want)
				}
			}
		})
	}
}

func TestQueueTasksListsLeasedFirstWithItem(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	_, sr1 := seedItem(t, s, "abc123xxxxx")
	_, sr2 := seedItem(t, s, "def456xxxxx")
	first := newTask(t, s, sr1, 3, nil)
	newTask(t, s, sr2, 3, nil)
	claim(t, s, claimOpts("w", 1))

	list, err := s.Read().QueueTasks(ctx, "builtin", 10*time.Minute)
	if err != nil || len(list) != 2 {
		t.Fatalf("QueueTasks = %v, %v", list, err)
	}
	if e := list[0]; e.ID != first.ID || e.State != TaskLeased || e.Platform != "youtube" || e.ExternalID != "abc123xxxxx" ||
		e.Stage != "metadata" || e.ResourceClass != "download" || e.Attempts != 1 || e.ItemID == "" {
		t.Errorf("leased 的任務應排在前面且帶有影音資訊：%+v", e)
	}
	if list[1].State != TaskPending {
		t.Errorf("第二筆應是 pending：%+v", list[1])
	}
	if other, _ := s.Read().QueueTasks(ctx, "agent", time.Minute); len(other) != 0 {
		t.Errorf("執行者不同不應列出：%v", other)
	}
}
