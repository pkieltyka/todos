package quickadd

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

// Remaining retains source bytes rather than reserialising parsed Markdown.
func Remaining(plan domain.QuickAddPlan, result domain.QuickAddResult) (string, []domain.DraftSpan) {
	var text strings.Builder
	spans := []domain.DraftSpan{}
	for index, item := range plan.Items {
		outcome := domain.QuickAddOutcome("unattempted")
		if index < len(result.Items) {
			outcome = result.Items[index].Result
		}
		if outcome == "created" || outcome == "skipped" {
			continue
		}
		start := text.Len()
		text.WriteString(item.Source)
		if outcome == "uncertain" {
			spans = append(spans, domain.DraftSpan{StartByte: start, EndByte: text.Len(), SourceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(item.Source)))})
		}
	}
	return text.String(), spans
}
