package servicetest

import (
	"context"
	"github.com/pkieltyka/todos/internal/domain"
)

func (f *Fake) PlanQuickAdd(a0 context.Context, a1 string, a2 domain.QuickAddOptions) (domain.QuickAddPlan, error) {
	return domain.QuickAddPlan{}, errNotBuilt()
}
func (f *Fake) RunQuickAdd(a0 context.Context, a1 domain.QuickAddPlan, a2 domain.QuickAddRunOptions) (domain.QuickAddResult, error) {
	return domain.QuickAddResult{}, errNotBuilt()
}
