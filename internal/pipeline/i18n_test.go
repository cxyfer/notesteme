package pipeline

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/apierr"
)

// NFR-I18N-2：估價逾時的訊息原樣成為 API 的錯誤訊息（UpstreamError），要在 apierr 的目錄中。
func TestEstimateBudgetMessageIsTranslated(t *testing.T) {
	if en, ok := apierr.Translate(errEstimateBudget.Error(), apierr.LangEn); !ok || en == errEstimateBudget.Error() {
		t.Errorf("%q 不在目錄中", errEstimateBudget.Error())
	}
}
