package domain

import (
	"testing"
	"time"
)

func TestChangeToken_EqualityIgnoresOnlyServerTime(t *testing.T) {
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	token := ChangeToken{Viewer: "viewer", IssuesTotal: 3, NewestIssue: 4, NewestUpdated: at, ProjectUpdated: at, ProjectItems: 2, ServerTime: at}
	for _, field := range []string{"viewer", "total", "number", "updated", "project", "items", "server", "timezone"} {
		t.Run(field, func(t *testing.T) {
			other := token
			switch field {
			case "viewer":
				other.Viewer = "someone"
			case "total":
				other.IssuesTotal++
			case "number":
				other.NewestIssue++
			case "updated":
				other.NewestUpdated = at.Add(time.Second)
			case "project":
				other.ProjectUpdated = at.Add(time.Second)
			case "items":
				other.ProjectItems++
			case "server":
				other.ServerTime = at.Add(time.Minute)
			case "timezone":
				other.NewestUpdated = at.In(time.FixedZone("offset", 3600))
			}
			want := field == "server" || field == "timezone"
			if token.Equal(other) != want || other.Equal(token) != want {
				t.Fatal("wrong equality", field)
			}
		})
	}
}
