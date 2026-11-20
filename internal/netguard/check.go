package netguard

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
)

// CheckURL 不連線，只檢查網址的目的地會不會被政策擋下，供新增時提早回報（ING-R6、GRD-R2）。
//
// 檢查：scheme 必須是 http、https；主機是 IP 字面值時直接套用 checkIP；否則用 Resolver 解析，
// 對每個 IP 套用 checkIP，全部被擋才算被擋（與 dial 一致）。被擋時錯誤符合 ErrBlockedAddress，
// scheme 不對時符合 ErrUnsupportedScheme，兩者都可以用 PolicyMessage 轉成訊息。
// 解析失敗（NXDOMAIN、逾時、沒有位址）原樣回傳，不算被擋。Resolver 為 nil 時回傳 ErrNoDialer。
//
// 這只是預檢：真正的連線仍然在 dial 時對實際要連的 IP 再檢查一次，DNS 在兩次之間改變也擋得住。
func CheckURL(ctx context.Context, d Deps, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("netguard: 網址不正確：%w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w：%q", ErrUnsupportedScheme, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("netguard: 網址沒有主機")
	}
	allow := d.mergedAllowlist()
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		if d.Resolver == nil {
			return ErrNoDialer
		}
		if ips, err = d.Resolver.LookupNetIP(ctx, "ip", host); err != nil {
			return err
		}
	}
	if len(ips) == 0 {
		return fmt.Errorf("netguard: no addresses for %q", host)
	}
	var blocked []error
	for _, ip := range ips {
		err := checkIP(host, ip.Unmap(), allow)
		if err == nil {
			return nil
		}
		blocked = append(blocked, fmt.Errorf("%w（主機 %s）", err, host))
	}
	return errors.Join(blocked...)
}
