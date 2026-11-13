package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// 獨立模式關閉失敗（例如租約放不回去、DB 關不掉）時不能回報成功（Codex 審查 PR #5）。
func TestCloseErrorTurnsSuccessIntoFailure(t *testing.T) {
	var stderr bytes.Buffer
	got := withCloseError(Result{Code: exitOK, Mode: ModeStandalone}, errors.New("release leases: boom"), &stderr)
	if got.Code != exitError || got.Mode != ModeStandalone {
		t.Errorf("Result = %+v，預期結束碼 1 並保留模式", got)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Errorf("stderr 應說明關閉的錯誤：%q", stderr.String())
	}
}

func TestCloseErrorKeepsMoreSpecificCode(t *testing.T) {
	var stderr bytes.Buffer
	if got := withCloseError(Result{Code: exitBlocked}, errors.New("x"), &stderr); got.Code != exitBlocked {
		t.Errorf("已經是非 0 的結束碼應保留：%+v", got)
	}
	if got := withCloseError(Result{Code: exitOK}, nil, &stderr); got.Code != exitOK {
		t.Errorf("沒有錯誤時不應改動：%+v", got)
	}
}
