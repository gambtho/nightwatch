// Package schedule is the workflow's fourth versioned artifact: when the
// job runs. Strictly parsed (fail closed, like the permit), and evaluated
// in the schedule's own IANA zone.
package schedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/robfig/cron/v3"
)

// parser accepts exactly the standard 5 fields — no seconds, no
// @-descriptors. The parser choice is part of the API contract.
var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

type Schedule struct {
	Cron string `json:"cron"`
	TZ   string `json:"tz"`

	spec cron.Schedule
	loc  *time.Location
}

func Parse(raw []byte) (*Schedule, error) {
	var s Schedule
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("schedule: %w", err)
	}
	if err := dec.Decode(new(struct{})); err != io.EOF {
		return nil, fmt.Errorf("schedule: trailing data after document")
	}
	if s.Cron == "" || s.TZ == "" {
		return nil, fmt.Errorf("schedule: cron and tz are both required")
	}
	if s.TZ == "Local" {
		return nil, fmt.Errorf("schedule: tz must be an IANA zone name, not Local")
	}
	loc, err := time.LoadLocation(s.TZ)
	if err != nil {
		return nil, fmt.Errorf("schedule: tz: %w", err)
	}
	spec, err := parser.Parse(s.Cron)
	if err != nil {
		return nil, fmt.Errorf("schedule: cron: %w", err)
	}
	s.spec = spec
	s.loc = loc
	return &s, nil
}

// Next returns the first occurrence strictly after the given instant,
// evaluated in the schedule's zone.
func (s *Schedule) Next(after time.Time) time.Time {
	return s.spec.Next(after.In(s.loc))
}
