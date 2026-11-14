// Package domain holds transport-independent todos data.
package domain

import "time"

type CheckedMode string // skip, close

type QuickAddOptions struct {
	Assignees, Labels []string
	AddToBoard        bool
	Status            *string
	Checked           CheckedMode
}

type QuickAddItem struct {
	Line               int
	Title, Body        string
	Checked            bool
	Source             string // exact source lines for this item, including body and separators
	StartByte, EndByte int    // half-open slice of original input
}

type Problem struct {
	Line    int
	Message string
}

type QuickAddPlan struct {
	Source                       string
	Items                        []QuickAddItem
	Options                      QuickAddOptions
	Problems                     []Problem
	ToCreate, ToSkip, WriteCount int
}

type QuickAddOutcome string // created, failed, uncertain, unattempted, skipped, planned

type QuickAddItemResult struct {
	Item            QuickAddItem
	Result          QuickAddOutcome
	Issue           *Issue
	OnBoard, Closed bool
	Error           *Error
}

type QuickAddCounts struct{ Created, Failed, Uncertain, Unattempted, Skipped, Planned int }

type QuickAddResult struct {
	Items  []QuickAddItemResult
	Counts QuickAddCounts
}

type QuickAddProgress struct {
	Index   int             // zero based, same item order as Plan.Items
	Phase   string          // creating, closing, placing, settled
	Outcome QuickAddOutcome // empty while a step is active
	Number  int             // zero until creation acknowledged
	Err     *Error
	Item    *QuickAddItemResult // immutable snapshot when settled
}

type QuickAddRunOptions struct {
	DryRun        bool
	StopRequested func() bool                  // thread-safe; checked only between complete items
	OnProgress    func(QuickAddProgress) error // synchronous, ordered; can fail draft save
}

type Draft struct {
	Version       int
	RepoKey, Text string
	UpdatedAt     time.Time
	Uncertain     []DraftSpan
	InFlight      *DraftSpan // crash while sending becomes uncertain on reload
	Revision      uint64
}

type DraftSpan struct {
	StartByte, EndByte int
	SourceSHA256       string
}
