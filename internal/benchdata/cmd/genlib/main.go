// genlib 產生 NFR 量測用的資料目錄（lib-10k 或 item-long，見 internal/benchdata）。
//
//	go run ./internal/benchdata/cmd/genlib -out /tmp/lib10k -profile lib-10k
//	go run ./internal/benchdata/cmd/genlib -out /tmp/lib -profile lib-10k -scale 0.05
//
// 產生的目錄可以直接給 notesteme 使用：NOTESTEME_DATA=<dir> notesteme serve，或 notesteme items ls --json。
// 完整的 lib-10k 約需數 GB 的磁碟空間與數分鐘到十幾分鐘；用完請自行刪除。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cxyfer/notesteme/internal/benchdata"
)

func main() {
	out := flag.String("out", "", "資料目錄（必填；不存在時建立，已有資料時拒絕）")
	profile := flag.String("profile", "lib-10k", "lib-10k 或 item-long")
	scale := flag.Float64("scale", 1, "影音數與頻道數的比例（0～1，開發與 smoke 用）")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "genlib: 需要 -out")
		os.Exit(2)
	}
	p, ok := benchdata.ProfileByName(*profile)
	if !ok {
		fmt.Fprintf(os.Stderr, "genlib: 不認識的 profile %q\n", *profile)
		os.Exit(2)
	}
	p = p.Scaled(*scale)
	if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 {
		fmt.Fprintf(os.Stderr, "genlib: %s 不是空目錄\n", *out)
		os.Exit(2)
	}
	start := time.Now()
	last := start
	res, err := benchdata.Generate(context.Background(), *out, p, benchdata.Options{
		Progress: func(done, total int) {
			if time.Since(last) >= 10*time.Second || done == total {
				last = time.Now()
				fmt.Fprintf(os.Stderr, "genlib: %d/%d 部（%s）\n", done, total, time.Since(start).Round(time.Second))
			}
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "genlib: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("genlib: %s → %s：%d 部、索引 %d 句，耗時 %s\n", p.Name, *out, len(res.ItemIDs), res.IndexedCues, time.Since(start).Round(time.Millisecond))
}
