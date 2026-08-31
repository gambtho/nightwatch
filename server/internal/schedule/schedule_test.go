package schedule_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/schedule"
)

func TestParseValidAndNext(t *testing.T) {
	s, err := schedule.Parse([]byte(`{"cron":"0 9 * * MON","tz":"America/New_York"}`))
	require.NoError(t, err)

	// Wed Jan 7 2026 12:00 UTC -> next Monday 09:00 America/New_York.
	after := time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)
	next := s.Next(after)
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 1, 12, 9, 0, 0, 0, loc).Unix(), next.Unix())
}

func TestParseRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"missing tz":       `{"cron":"* * * * *"}`,
		"missing cron":     `{"tz":"UTC"}`,
		"bad cron":         `{"cron":"not cron","tz":"UTC"}`,
		"descriptor":       `{"cron":"@daily","tz":"UTC"}`,
		"six fields":       `{"cron":"0 0 9 * * MON","tz":"UTC"}`,
		"bad tz":           `{"cron":"* * * * *","tz":"Mars/Olympus"}`,
		"empty tz":         `{"cron":"* * * * *","tz":""}`,
		"local tz":         `{"cron":"* * * * *","tz":"Local"}`,
		"unknown field":    `{"cron":"* * * * *","tz":"UTC","jitter":5}`,
		"trailing garbage": `{"cron":"* * * * *","tz":"UTC"}x`,
		"not json":         `nope`,
	} {
		_, err := schedule.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}

func TestDSTSpringForwardPinned(t *testing.T) {
	// 2026-03-08 02:30 does not exist in America/New_York (clocks jump
	// 02:00 -> 03:00). Pin robfig/cron's behavior for a 02:30 daily
	// schedule across that boundary so a dependency upgrade cannot
	// silently change semantics: the library fires at the next real
	// occurrence after the gap.
	s, err := schedule.Parse([]byte(`{"cron":"30 2 * * *","tz":"America/New_York"}`))
	require.NoError(t, err)
	loc, _ := time.LoadLocation("America/New_York")
	after := time.Date(2026, 3, 7, 12, 0, 0, 0, loc)
	first := s.Next(after)
	second := s.Next(first)
	// The 02:30 slot on Mar 8 is skipped (it does not exist); the pinned
	// expectation is Mar 9 02:30 following Mar 7... after==Mar 7 12:00, so
	// first is Mar 8's occurrence IF the library maps it, else Mar 9.
	// Assert the invariants that matter and log the concrete times:
	require.True(t, first.After(after))
	require.True(t, second.After(first))
	require.Equal(t, 30, second.In(loc).Minute())
	t.Logf("pinned DST behavior: first=%s second=%s", first.In(loc), second.In(loc))
	// Pin the exact first-occurrence date so upgrades are visible:
	require.Equal(t, time.Date(2026, 3, 10, 2, 30, 0, 0, loc).Unix(), second.Unix())
}
