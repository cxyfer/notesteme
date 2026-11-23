package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/config"
)

func opml(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<opml version=\"2.0\">\n<head><title>t</title></head>\n<body>\n" + body + "</body>\n</opml>\n")
}

// OPML：分類 outline 底下的也收集；檔案內重複、與既有的重複列在 duplicates（原文、依檔案順序）；
// 非 http(s) 的列在 failed，不影響其他列；內部網路的網址匯入時不連外，照樣建立（檢查時由 netguard 擋下）。
func TestImportOPML(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	if _, err := r.svc.Create(ctx, CreateInput{URL: "https://feeds.example.com/c.xml"}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("長", 400)
	data := opml(`<outline text="Tech">
  <outline type="rss" text="Show A" xmlUrl="https://feeds.example.com/a.xml"/>
  <outline type="rss" text="ignored" title="Show &lt;b&gt;B&lt;/b&gt;" xmlUrl="https://FEEDS.example.com:443/b.xml#x"/>
</outline>
<outline type="rss" text="Show A again" xmlUrl="https://feeds.example.com/a.xml"/>
<outline type="rss" text="Show C" xmlUrl="https://feeds.example.com/c.xml"/>
<outline type="rss" text="Bad" xmlUrl="ftp://feeds.example.com/d.xml"/>
<outline type="rss" text="Local" xmlUrl="file:///etc/passwd"/>
<outline type="rss" text="YT feed" xmlUrl="https://www.youtube.com/feeds/videos.xml?channel_id=UCfixture000000000000000"/>
<outline type="rss" text="Internal" xmlUrl="http://10.0.0.5/feed.xml"/>
<outline type="rss" text="` + long + `" xmlUrl="https://feeds.example.com/long.xml"/>
<outline type="rss" text="Too long URL" xmlUrl="https://feeds.example.com/` + strings.Repeat("x", MaxURLLen) + `"/>
<outline text="no url"/>
`)
	res, err := r.svc.Import(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 4 {
		t.Errorf("created = %d，預期 4（a、b、internal、long）", res.Created)
	}
	if !eq(res.Duplicates, []string{"https://feeds.example.com/a.xml", "https://feeds.example.com/c.xml"}) {
		t.Errorf("duplicates = %v", res.Duplicates)
	}
	var failed []string
	for _, f := range res.Failed {
		failed = append(failed, fmt.Sprintf("%d:%s", f.Line, f.URL[:min(len(f.URL), 40)]))
		if f.Message == "" || strings.Contains(f.Message, "passwd") {
			t.Errorf("失敗訊息：%q", f.Message)
		}
	}
	want := []string{"11:ftp://feeds.example.com/d.xml", "12:file:///etc/passwd", "13:https://www.youtube.com/feeds/videos.xml", "16:https://feeds.example.com/xxxxxxxxxxxxxx"}
	if !eq(failed, want) {
		t.Errorf("failed = %v，預期 %v", failed, want)
	}
	if res.Failed[3].Message != msgURLTooLong || res.Failed[0].Message != msgUnsupportedURL {
		t.Errorf("訊息：%+v", res.Failed)
	}
	subs, err := r.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, s := range subs {
		titles[s.URL] = s.Title
		if s.Backfill.Mode != BackfillNone || s.Profile != nil || !s.Filters.ExcludeShorts || s.Filters.Live != LiveWait || s.NextCheckAt == nil {
			t.Errorf("匯入的訂閱要用預設值：%+v", s)
		}
	}
	if titles["https://feeds.example.com/a.xml"] != "Show A" || titles["https://feeds.example.com/b.xml"] != "Show B" {
		t.Errorf("標題：%v", titles)
	}
	if lt := titles["https://feeds.example.com/long.xml"]; utf8.RuneCountInString(lt) != maxTitleRunes+1 || !strings.HasSuffix(lt, "…") {
		t.Errorf("長標題要截斷：%d", utf8.RuneCountInString(lt))
	}
	if _, ok := titles["http://10.0.0.5/feed.xml"]; !ok {
		t.Errorf("內部網路的網址匯入時不連外，照樣建立：%v", titles)
	}
}

// Google Takeout：網址以 Channel Id 組成；BOM、CRLF 都可以；欄位數不對、ID 不合法的列在 failed。
func TestImportTakeout(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	data := "\xef\xbb\xbfChannel Id,Channel Url,Channel Title\r\n" +
		testChannel + ",http://www.youtube.com/channel/" + testChannel + ",Fixture Channel\r\n" +
		testChannel2 + ",,\"Second, Channel\"\r\n" +
		testChannel + ",http://www.youtube.com/channel/" + testChannel + ",again\r\n" +
		"bad,http://www.youtube.com/channel/bad,Bad\r\n" +
		"only-two,fields\r\n" +
		",,\r\n" +
		",https://www.youtube.com/@handle,Handle\r\n"
	res, err := r.svc.Import(ctx, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 3 || !eq(res.Duplicates, []string{"http://www.youtube.com/channel/" + testChannel}) {
		t.Errorf("結果：%+v", res)
	}
	var failed []string
	for _, f := range res.Failed {
		failed = append(failed, fmt.Sprintf("%d:%s:%s", f.Line, f.URL, f.Message))
	}
	want := []string{
		"5:http://www.youtube.com/channel/bad:" + msgUnsupportedURL,
		"6:fields:" + msgImportBadRow,
		"7::" + msgImportURLMissing,
	}
	if !eq(failed, want) {
		t.Errorf("failed = %q\n預期 %q", failed, want)
	}
	subs, _ := r.svc.List(ctx)
	if len(subs) != 3 || subs[0].ExternalID != testChannel || subs[0].Title != "Fixture Channel" || subs[0].URL != "https://www.youtube.com/channel/"+testChannel ||
		subs[1].Title != "Second, Channel" || subs[2].ExternalID != "@handle" {
		t.Errorf("訂閱：%+v", subs)
	}
}

// 整份拒絕的檔案：不建立任何訂閱。含 DOCTYPE 的（XXE、billion laughs）不展開任何 entity。
func TestImportRejectsWholeFile(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	many := func(n int) []byte {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, `<outline type="rss" text="s" xmlUrl="https://feeds.example.com/%d.xml"/>`+"\n", i)
		}
		return opml(b.String())
	}
	nested := func(depth int) []byte {
		return opml(strings.Repeat("<outline text=\"x\">", depth) + `<outline xmlUrl="https://feeds.example.com/deep.xml"/>` + strings.Repeat("</outline>", depth))
	}
	cases := []struct {
		name string
		data []byte
		code string
		msg  string
	}{
		{"xxe", []byte(`<?xml version="1.0"?><!DOCTYPE opml [<!ENTITY e SYSTEM "file:///etc/passwd">]><opml><body><outline text="&e;" xmlUrl="https://feeds.example.com/a.xml"/></body></opml>`), CodeImportFormat, msgImportFormat},
		{"billion laughs", []byte(`<?xml version="1.0"?><!DOCTYPE opml [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;"><!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">]><opml><body><outline text="&c;" xmlUrl="https://feeds.example.com/a.xml"/></body></opml>`), CodeImportFormat, msgImportFormat},
		{"doctype 小寫", []byte(`<!doctype opml><opml><body/></opml>`), CodeImportFormat, msgImportFormat},
		{"未定義的 entity", opml(`<outline text="&x;" xmlUrl="https://feeds.example.com/a.xml"/>`), CodeImportFormat, msgImportFormat},
		{"根元素不是 opml", []byte(`<rss><channel/></rss>`), CodeImportFormat, msgImportFormat},
		{"兩個根元素", []byte(`<opml/><opml/>`), CodeImportFormat, msgImportFormat},
		{"沒有結束", []byte(`<opml><body>`), CodeImportFormat, msgImportFormat},
		{"巢狀過深", nested(maxImportDepth), CodeImportFormat, msgImportFormat},
		{"非 UTF-8", append([]byte(`<opml><body><outline text="`), 0xff, '"', '/', '>'), CodeImportFormat, msgImportFormat},
		{"宣告其他編碼", []byte(`<?xml version="1.0" encoding="ISO-8859-1"?><opml><body/></opml>`), CodeImportFormat, msgImportFormat},
		{"CSV 表頭不符", []byte("Channel Id,Channel Title\nUC,x\n"), CodeImportFormat, msgImportFormat},
		{"CSV 引號錯誤", []byte("Channel Id,Channel Url,Channel Title\n\"UC,x,y\n"), CodeImportFormat, msgImportFormat},
		{"空檔案", nil, CodeImportFormat, msgImportFormat},
		{"501 個", many(501), CodeImportTooMany, "匯入檔最多 500 個訂閱，這個檔案有 501 個。"},
		{"超過 1 MiB", append(many(1), make([]byte, MaxImportBytes)...), CodeImportTooLarge, msgImportTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := r.svc.Import(ctx, c.data)
			var ie *ImportError
			if !errors.As(err, &ie) || ie.Code != c.code || ie.Message != c.msg || ie.Error() != c.msg {
				t.Fatalf("err = %v，預期 %s", err, c.code)
			}
			if n, _ := r.svc.Count(ctx); n != 0 {
				t.Fatalf("整份拒絕時不能建立訂閱：%d", n)
			}
		})
	}
	// 深度剛好在上限內、500 個剛好在上限內：可以匯入。
	if res, err := r.svc.Import(ctx, nested(maxImportDepth-3)); err != nil || res.Created != 1 {
		t.Fatalf("深度上限內：%+v %v", res, err)
	}
	if res, err := r.svc.Import(ctx, many(MaxImportEntries)); err != nil || res.Created != MaxImportEntries {
		t.Fatalf("500 個：created %d, %v", res.Created, err)
	}
}

// 部分失敗不回滾：處理到一半遇到無法分類的錯誤時回傳錯誤，已經建立的保留。
func TestImportKeepsCreatedOnError(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	r.ing.plan = nil
	res, err := r.svc.Import(ctx, opml(`<outline xmlUrl="https://feeds.example.com/1.xml"/><outline xmlUrl="https://feeds.example.com/2.xml"/>`))
	if err != nil || res.Created != 2 {
		t.Fatal(res, err)
	}
	boom := errors.New("處理方案壞了")
	r.ing.plan = boom
	res, err = r.svc.Import(ctx, opml(`<outline xmlUrl="https://feeds.example.com/3.xml"/>`))
	if !errors.Is(err, boom) || res.Created != 0 {
		t.Fatalf("錯誤要回傳：%+v %v", res, err)
	}
	if n, _ := r.svc.Count(ctx); n != 2 {
		t.Fatalf("先前建立的要保留：%d", n)
	}
	cctx, cancel := contextCancelled(t)
	defer cancel()
	if _, err := r.svc.Import(cctx, opml(`<outline xmlUrl="https://feeds.example.com/4.xml"/>`)); err == nil {
		t.Fatal("取消時要停止")
	}
}

// FuzzParseImport：任意輸入都不能 panic；接受的檔案列數不超過上限，每列都有行號。
func FuzzParseImport(f *testing.F) {
	f.Add(opml(`<outline xmlUrl="https://feeds.example.com/a.xml"/>`))
	f.Add([]byte("Channel Id,Channel Url,Channel Title\nUCfixture000000000000000,,x\n"))
	f.Add([]byte(`<!DOCTYPE x><opml/>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		rows, err := parseImport(data)
		if err != nil {
			var ie *ImportError
			if !errors.As(err, &ie) {
				t.Fatalf("錯誤型別 %T", err)
			}
			return
		}
		if len(rows) > MaxImportEntries {
			t.Fatalf("列數 %d", len(rows))
		}
		for _, r := range rows {
			if r.line <= 0 {
				t.Fatalf("行號 %d", r.line)
			}
		}
	})
}

func contextCancelled(t *testing.T) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx, cancel
}
