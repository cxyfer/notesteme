package cookies

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeHeaderFormat(t *testing.T) {
	got, n, err := Normalize("youtube", "SID=abc123; HSID=def456")
	if err != nil {
		t.Fatal(err)
	}
	want := netscapeHeader +
		".youtube.com\tTRUE\t/\tTRUE\t0\tSID\tabc123\n" +
		".youtube.com\tTRUE\t/\tTRUE\t0\tHSID\tdef456\n"
	if got != want || n != 2 {
		t.Fatalf("Normalize = %q, %d", got, n)
	}

	// 瀏覽器複製出來的標頭常帶 "Cookie:" 前綴與多餘空白。
	got, n, err = Normalize("bilibili", "  Cookie: SESSDATA=s%2Cx; bili_jct=j1 ;\n")
	if err != nil || n != 2 || !strings.Contains(got, ".bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\ts%2Cx\n") {
		t.Fatalf("Normalize = %q, %d, %v", got, n, err)
	}
}

func TestNormalizeNetscapeFormat(t *testing.T) {
	in := "# Netscape HTTP Cookie File\n# 註解\n\n" +
		".youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tabc\n" +
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tHSID\tdef\n" +
		"www.youtube.com\tFALSE\t/watch\tFALSE\t1893456000\tPREF\tf=1\n"
	got, n, err := Normalize("youtube", in)
	if err != nil || n != 3 {
		t.Fatalf("Normalize = %q, %d, %v", got, n, err)
	}
	for _, want := range []string{
		netscapeHeader,
		".youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tabc\n",
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tHSID\tdef\n",
		"www.youtube.com\tFALSE\t/watch\tFALSE\t1893456000\tPREF\tf=1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("輸出缺少 %q：%q", want, got)
		}
	}
}

func TestNormalizeDropsOtherDomains(t *testing.T) {
	in := ".youtube.com\tTRUE\t/\tTRUE\t0\tSID\tkeep1\n" +
		".google.com\tTRUE\t/\tTRUE\t0\tSIDCC\tdrop1\n" +
		".notyoutube.com\tTRUE\t/\tTRUE\t0\tX\tdrop2\n" +
		"youtube.com.evil.example\tFALSE\t/\tTRUE\t0\tY\tdrop3\n" +
		".bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\tdrop4\n"
	got, n, err := Normalize("youtube", in)
	if err != nil || n != 1 {
		t.Fatalf("Normalize = %q, %d, %v", got, n, err)
	}
	for _, bad := range []string{"drop1", "drop2", "drop3", "drop4"} {
		if strings.Contains(got, bad) {
			t.Errorf("輸出不應含其他網域的 cookie %q：%q", bad, got)
		}
	}
	if !strings.Contains(got, "keep1") {
		t.Errorf("輸出缺少 youtube 的 cookie：%q", got)
	}
}

func TestNormalizeErrors(t *testing.T) {
	cases := []struct {
		name, platform, content string
		want                    error
	}{
		{"沒有剩下任何 cookie", "youtube", ".google.com\tTRUE\t/\tTRUE\t0\tSIDCC\tsecret-value\n", ErrNoCookies},
		{"bilibili 缺 SESSDATA", "bilibili", "bili_jct=secret-value; DedeUserID=1", ErrMissingSESSDATA},
		{"平台不認識", "twitter", "a=secret-value", ErrUnknownPlatform},
		{"平台含斜線", "youtube/../x", "a=secret-value", ErrUnknownPlatform},
		{"空內容", "youtube", "", ErrEmpty},
		{"只有空白", "youtube", " \n\t \n", ErrEmpty},
		{"超過 64 KiB", "youtube", "a=" + strings.Repeat("x", MaxContentSize), ErrTooLarge},
		{"標頭格式缺名稱", "youtube", "=secret-value", ErrNoCookies},
		{"標頭格式不是 name=value", "youtube", "secret-value", ErrNoCookies},
		{"值含控制字元", "youtube", "a=sec\x01ret-value", ErrInvalidFormat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n, err := Normalize(c.platform, c.content)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v，預期 %v", err, c.want)
			}
			if got != "" || n != 0 {
				t.Errorf("失敗時不應有輸出：%q, %d", got, n)
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Errorf("錯誤訊息含 cookie 的值：%v", err)
			}
		})
	}
}

func TestNormalizeSizeBoundary(t *testing.T) {
	// 剛好 64 KiB 是允許的。
	content := "a=" + strings.Repeat("x", MaxContentSize-2)
	if _, _, err := Normalize("youtube", content); err != nil {
		t.Fatalf("剛好 %d bytes 應該通過：%v", MaxContentSize, err)
	}
}

func TestNormalizeBilibiliWithSESSDATAInNetscape(t *testing.T) {
	in := ".bilibili.com\tTRUE\t/\tTRUE\t1893456000\tSESSDATA\tabc\n.bilibili.com\tTRUE\t/\tTRUE\t0\tbili_jct\tj\n"
	_, n, err := Normalize("bilibili", in)
	if err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
}
