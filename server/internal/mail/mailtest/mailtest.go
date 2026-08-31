// Package mailtest provides a recording mail.Sender for tests, following
// the testpg/llmtest pattern of keeping test doubles out of production
// packages.
package mailtest

import (
	"context"
	"sync"
)

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
