// Package captureverify is the control-plane paste-time token check: it
// invokes a connector's declared verify op with the candidate secret —
// which is deliberately NOT yet in the vault — before the connection is
// stored. It is session-authed territory only; the run-path proxy never
// calls it. Compilation is shared with the proxy (catalog.Compile), so
// the request a verify sends is the request a run would send.
package captureverify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gambtho/tomte/server/internal/catalog"
)

// Result is what the paste surface shows: verified or not, user-facing
// copy when not, and the scopes the installed app is missing (a warning,
// never a failure — the spec's warn-and-store posture).
type Result struct {
	OK            bool
	Message       string
	MissingScopes []string
}

const (
	requestTimeout = 10 * time.Second
	maxBodyBytes   = 1 << 20
)

// Client makes verify calls. The zero value is ready to use.
type Client struct {
	// Upstreams overrides the upstream scheme+host per connector id —
	// tests point catalog hosts at a local fake, mirroring the proxy's
	// ConnectorUpstreams. The transport posture itself (proxy selection
	// disabled, redirects refused, timeout) is deliberately not
	// swappable.
	Upstreams map[string]string
}

func (c *Client) httpClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		// Proxy selection disabled: HTTP(S)_PROXY must not route a
		// credential-bearing call through an unvetted intermediary.
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("redirects are refused")
		},
	}
}

// Verify runs the connector's verify op with the candidate secret. A
// non-nil error is an authoring or programming mistake (no verify op,
// uncompilable binding); everything the user can act on comes back in
// Result.
func (c *Client) Verify(ctx context.Context, con *catalog.Connector, secret string) (Result, error) {
	guide := con.Auth.Capture
	if guide == nil || guide.VerifyOp == "" {
		return Result{}, fmt.Errorf("captureverify: connector %q declares no verify op", con.ID)
	}
	op, ok := con.Op(guide.VerifyOp)
	if !ok {
		return Result{}, fmt.Errorf("captureverify: verify op %q missing", guide.VerifyOp)
	}

	// The instant wrong-string-paste check, server-side too — no
	// credential-bearing call for a value that cannot be the token.
	if guide.SecretPrefix != "" && !strings.HasPrefix(secret, guide.SecretPrefix) {
		return Result{Message: fmt.Sprintf(
			"That doesn't look like the right value — the token starts with %s. Copy it again and re-paste.",
			guide.SecretPrefix)}, nil
	}

	args, err := op.Schema().Validate([]byte("{}"))
	if err != nil {
		return Result{}, fmt.Errorf("captureverify: verify op %q takes args: %w", guide.VerifyOp, err)
	}
	compiled, err := catalog.Compile(op, args)
	if err != nil {
		return Result{}, fmt.Errorf("captureverify: %w", err)
	}
	target := compiled.URL
	if base, ok := c.Upstreams[con.ID]; ok {
		target, err = rewriteUpstream(target, base)
		if err != nil {
			return Result{}, err
		}
	}

	var body io.Reader
	if compiled.Body != nil {
		body = strings.NewReader(string(compiled.Body))
	}
	req, err := http.NewRequestWithContext(ctx, compiled.Method, target, body)
	if err != nil {
		return Result{}, fmt.Errorf("captureverify: %w", err)
	}
	// The proxy's outbound header posture: built from scratch, nothing
	// else forwarded.
	req.Header.Set("Accept", "application/json")
	if compiled.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+secret)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The operator-facing distinction the user message can't carry:
		// "our egress is broken" is not "the token is wrong".
		slog.Warn("captureverify: upstream unreachable", "connector", con.ID, "err", err)
		return Result{Message: fmt.Sprintf("Couldn't reach %s to check the token. Check your connection and try again.", con.Name)}, nil
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))

	// Slack-style Web API envelope; also parsed on error statuses, where
	// the body's error code is the only actionable detail.
	var envelope struct {
		OK    *bool  `json:"ok"`
		Error string `json:"error"`
	}
	parseErr := json.Unmarshal(raw, &envelope)

	rejected := func() Result {
		msg := fmt.Sprintf("%s didn't accept this token. Copy it again and re-paste — a character may be missing.", con.Name)
		if parseErr == nil && envelope.Error != "" {
			msg += fmt.Sprintf(" (%s)", envelope.Error)
		}
		return Result{Message: msg}
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// An auth-status rejection is a re-paste, never a retry.
		return rejected(), nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return Result{Message: fmt.Sprintf("%s returned an error (HTTP %d) while checking the token. Try again in a moment.", con.Name, resp.StatusCode)}, nil
	}

	// Fail closed on a 2xx we cannot read: a truncated body, or a WAF /
	// TLS-intercepting-proxy interstitial serving HTML with a 200, must
	// never verify a token. A parseable JSON body without a boolean ok
	// field is a plain non-envelope API and passes on status.
	if readErr != nil || parseErr != nil {
		slog.Warn("captureverify: unreadable 2xx verify response",
			"connector", con.ID, "status", resp.StatusCode, "read_err", readErr, "parse_err", parseErr)
		return Result{Message: fmt.Sprintf("%s sent an unexpected response while checking the token. Try again in a moment.", con.Name)}, nil
	}
	if envelope.OK != nil && !*envelope.OK {
		return rejected(), nil
	}

	return Result{OK: true, MissingScopes: missingScopes(con, resp.Header.Get("x-oauth-scopes"))}, nil
}

// missingScopes diffs the union of the connector's declared op scopes
// against the granted set the upstream reported. An absent header means
// the upstream doesn't report scopes: no warning, never a guess.
func missingScopes(con *catalog.Connector, granted string) []string {
	if granted == "" {
		return nil
	}
	have := map[string]bool{}
	for _, s := range strings.Split(granted, ",") {
		have[strings.TrimSpace(s)] = true
	}
	missing := map[string]bool{}
	for _, op := range con.Ops {
		for _, s := range op.Scopes {
			if !have[s] {
				missing[s] = true
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	out := make([]string, 0, len(missing))
	for s := range missing {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// rewriteUpstream swaps scheme+host of a compiled URL for a test
// override, keeping path and query.
func rewriteUpstream(compiled, base string) (string, error) {
	cu, err := url.Parse(compiled)
	if err != nil {
		return "", fmt.Errorf("captureverify: %w", err)
	}
	bu, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("captureverify: bad upstream override: %w", err)
	}
	cu.Scheme = bu.Scheme
	cu.Host = bu.Host
	return cu.String(), nil
}
