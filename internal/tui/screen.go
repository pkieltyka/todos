package tui

import (
	tea "charm.land/bubbletea/v2"
)

// ViewID is a top-level view (tab).
type ViewID int

const (
	ViewIssues ViewID = iota
	ViewPRs
	ViewBoard
)

// ScreenID names a screen or overlay, for help texts and titles.
type ScreenID int

const (
	ScreenIssues ScreenID = iota
	ScreenIssue
	ScreenPRs
	ScreenPR
	ScreenBoard
	ScreenBoardSetup
	ScreenHelp
)

// InputMode is navigation (letters are commands) or text (letters are text).
type InputMode int

const (
	ModeNav InputMode = iota
	ModeText
)

// ActionMsg delivers a resolved key action to a screen or overlay.
type ActionMsg struct{ A Action }

// Screen lives in a tab's stack (tui.md 4.2). Screens are pointers and
// change their own UI state in place; data stays in the store.
type Screen interface {
	ID() ScreenID
	// Update handles ActionMsg in navigation mode, raw keys in text mode, and
	// any other message the App forwards.
	Update(c *Ctx, msg tea.Msg) tea.Cmd
	// Sync recomputes derived state (rows, viewport content) after every
	// Update, so View stays a pure function of state.
	Sync(c *Ctx)
	View(c *Ctx, area Rect) string
	SubBar(c *Ctx, w int) string
	Actions(c *Ctx) []Action
	Mode() InputMode
	Title(c *Ctx) string
	Position(c *Ctx) string
	// Cursor is the real terminal cursor in text mode, in frame coordinates.
	Cursor(c *Ctx) *tea.Cursor
	// Slots are the reads to cancel when the screen is popped.
	Slots() []Slot
	// Renders lists the Markdown the screen wants rendered.
	Renders(c *Ctx) []renderReq
}

// Overlay sits on top of every screen; the top overlay has focus
// (tui.md 4.3). Phase 3 has only Help.
type Overlay interface {
	Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult)
	View(c *Ctx, body Rect) (content string, box Rect)
	Actions(c *Ctx) []Action
	Mode() InputMode
	Cursor(c *Ctx) *tea.Cursor
	Busy() bool
}

// OverlayResult tells the App whether to keep the overlay.
type OverlayResult int

const (
	Keep OverlayResult = iota
	Close
)

// renderReq asks for one Markdown render.
type renderReq struct {
	Src   string
	Width int
}
