// Package mail is the transactional-email seam. The provider choice
// (Postmark/SES/Resend-class, shared with Plan 4 alerting) is an open
// question in the identity spec; until it is answered, LogSender is the
// development delivery path and Sender is the only thing the rest of the
// server knows about.
package mail

import (
	"context"
	"log/slog"
	"sync"
)

type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// LogSender writes the message to the log. In development the logged
// magic link IS the login flow, so the body is logged deliberately.
type LogSender struct{}

func (LogSender) Send(_ context.Context, to, subject, body string) error {
	slog.Info("mail: send", "to", to, "subject", subject, "body", body)
	return nil
}

// Recorder is a test Sender that captures messages.
type Recorder struct {
	mu   sync.Mutex
	msgs []Message
}

type Message struct {
	To      string
	Subject string
	Body    string
}

func (r *Recorder) Send(_ context.Context, to, subject, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, Message{To: to, Subject: subject, Body: body})
	return nil
}

func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.msgs...)
}
