package store

import (
	"database/sql/driver"
	"fmt"

	"modernc.org/sqlite"
)

// foldTitleFunc 是 SQL 中呼叫 FoldTitle 的函式名稱。
// migration 00006 用它回填既有的 title_fold，讓舊資料與之後寫入的資料用同一套 fold（LIB-R3）。
const foldTitleFunc = "go_fold_title"

func init() {
	// 註冊對之後開啟的所有連線生效；store 的連線都在 init 之後才開啟。
	sqlite.MustRegisterDeterministicScalarFunction(foldTitleFunc, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case nil:
			return nil, nil
		case string:
			return FoldTitle(v), nil
		case []byte:
			return FoldTitle(string(v)), nil
		default:
			return nil, fmt.Errorf("%s 只接受文字，收到 %T", foldTitleFunc, v)
		}
	})
}
