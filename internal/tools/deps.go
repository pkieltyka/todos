//go:build deps

// Package tools retains the approved dependency pins until their production imports exist.
package tools

import (
	_ "charm.land/bubbles/v2/textarea"
	_ "charm.land/bubbletea/v2"
	_ "charm.land/glamour/v2"
	_ "charm.land/lipgloss/v2"
	_ "github.com/charmbracelet/x/ansi"
	_ "github.com/charmbracelet/x/exp/golden"
	_ "github.com/charmbracelet/x/exp/teatest/v2"
	_ "github.com/cli/go-gh/v2"
	_ "github.com/google/shlex"
	_ "github.com/spf13/cobra"
	_ "github.com/yuin/goldmark"
	_ "go.yaml.in/yaml/v3"
	_ "golang.org/x/net/html"
	_ "golang.org/x/term"
)
