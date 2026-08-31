// Package mail is the transactional-email seam. The identity spec left
// the provider open; the grading + alerting spec (PR #13) settled it on
// Postmark for the whole platform. LogSender remains the development
// delivery path, and Sender is the only thing the rest of the server
// knows about.
package mail

import (
	"context"
	"errors"
	"log/slog"
)

type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Select picks the delivery path from configuration. Both Postmark values
// set → Postmark; exactly one set → an error (a deployment mistake, not a
// dev setup); neither set → LogSender, but only when the caller says a
// dev fallback is acceptable (a localhost deployment) — a production
// server with no mail configured must fail at startup, not silently log
// every login link.
func Select(postmarkToken, from string, devFallbackOK bool) (Sender, error) {
	switch {
	case postmarkToken != "" && from != "":
		return NewPostmark(postmarkToken, from), nil
	case postmarkToken != "" || from != "":
		return nil, errors.New("mail: TOMTE_POSTMARK_TOKEN and TOMTE_MAIL_FROM must be set together")
	case devFallbackOK:
		return LogSender{}, nil
	default:
		return nil, errors.New("mail: no provider configured; set TOMTE_POSTMARK_TOKEN and TOMTE_MAIL_FROM (log delivery is only allowed for a localhost base URL)")
	}
}

// LogSender writes the message to the log. In development the logged
// magic link IS the login flow, so the body is logged deliberately.
type LogSender struct{}

func (LogSender) Send(_ context.Context, to, subject, body string) error {
	slog.Info("mail: send", "to", to, "subject", subject, "body", body)
	return nil
}
