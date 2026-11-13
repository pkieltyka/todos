// Package service defines the shared CLI and TUI use-case contracts.
package service

import (
	"context"
	"github.com/pkieltyka/todos/internal/domain"
)

type SettingsStore interface {
	RememberedBoard(context.Context, domain.RepoRef) (*domain.ProjectRef, error)
	RememberBoard(context.Context, domain.RepoRef, domain.ProjectRef) error
	Check(context.Context) (domain.DoctorCheck, error)
}
