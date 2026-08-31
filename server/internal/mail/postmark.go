package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Postmark sends via the Postmark transactional API — the platform's one
// email provider (chosen by the grading + alerting spec; shared with its
// alert delivery).
type Postmark struct {
	// BaseURL is overridable for tests; the zero value uses the real API.
	BaseURL string
	Client  *http.Client

	token string
	from  string
}

func NewPostmark(serverToken, from string) *Postmark {
	return &Postmark{
		BaseURL: "https://api.postmarkapp.com",
		Client:  &http.Client{Timeout: 10 * time.Second},
		token:   serverToken,
		from:    from,
	}
}

func (p *Postmark) Send(ctx context.Context, to, subject, body string) error {
	payload, err := json.Marshal(map[string]string{
		"From":          p.from,
		"To":            to,
		"Subject":       subject,
		"TextBody":      body,
		"MessageStream": "outbound",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/email", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Postmark-Server-Token", p.token)

	resp, err := p.Client.Do(req)
	if err != nil {
		return fmt.Errorf("postmark: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("postmark: status %d: %s", resp.StatusCode, msg)
	}
	return nil
}
