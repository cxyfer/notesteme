package auth

import (
	"context"
	"errors"
)

// ErrEmptyNewPassword 表示新密碼是空的。
var ErrEmptyNewPassword = errors.New("新密碼不能是空的。")

// ChangePassword 驗證目前的密碼後改成 newPassword（AUTH-R2 的「設定頁修改密碼」）。
//
//   - 目前密碼的驗證和登入共用同一套鎖定（AUTH-R6）：錯誤計入 ip 的失敗次數，鎖定期間回傳 *LockedError，
//     避免這個路由變成繞過鎖定的猜密碼入口。錯誤一律是 ErrInvalidPassword。
//   - 新密碼只要求非空（和 EnsurePassword 的 NOTESTEME_PASSWORD 一致）。
//   - 成功後撤銷 keepSession 以外的所有 session（其他裝置立即登出）；keepSession 是目前請求的 session token，
//     本機 socket 等沒有 session 的呼叫傳空字串，所有 session 都會撤銷。
func (s *Service) ChangePassword(ctx context.Context, current, newPassword, ip, keepSession string) error {
	if newPassword == "" {
		return ErrEmptyNewPassword
	}
	if retry := s.locks.remaining(ip, s.clock.Now()); retry > 0 {
		return &LockedError{RetryAfter: retry}
	}
	// 和 Login 共用臨界區：同時只有一個 argon2 驗證，鎖定判斷與失敗計數不會被並行請求繞過。
	s.loginMu.Lock()
	defer s.loginMu.Unlock()

	if retry := s.locks.remaining(ip, s.clock.Now()); retry > 0 {
		return &LockedError{RetryAfter: retry}
	}
	encoded, ok, err := s.store.GetSetting(ctx, settingPasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoPassword
	}
	match, err := s.verify(current, encoded)
	if err != nil {
		return err
	}
	if !match {
		s.locks.fail(ip, s.clock.Now())
		return ErrInvalidPassword
	}
	s.locks.reset(ip)

	h, err := hashPassword(newPassword, s.params)
	if err != nil {
		return err
	}
	keep := ""
	if keepSession != "" {
		keep = hashToken(keepSession)
	}
	n, err := s.store.ReplacePasswordHash(ctx, settingPasswordHash, h, keep)
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "已修改登入密碼", "revoked_sessions", n)
	return nil
}
