package mail_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/mail"
)

func TestRecorderCapturesSends(t *testing.T) {
	rec := &mail.Recorder{}
	require.NoError(t, rec.Send(context.Background(), "pat@acme.test", "Sign in", "https://x/auth/verify?token=abc"))
	msgs := rec.Messages()
	require.Len(t, msgs, 1)
	require.Equal(t, "pat@acme.test", msgs[0].To)
	require.Contains(t, msgs[0].Body, "token=abc")
}

func TestPostmarkSendsExpectedRequest(t *testing.T) {
	var gotToken string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Postmark-Server-Token")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ErrorCode":0,"Message":"OK"}`))
	}))
	defer srv.Close()

	p := mail.NewPostmark("server-token", "login@nightshift.test")
	p.BaseURL = srv.URL
	require.NoError(t, p.Send(context.Background(), "pat@acme.test", "Sign in", "https://x/auth/verify?token=abc"))
	require.Equal(t, "server-token", gotToken)
	require.Equal(t, "pat@acme.test", gotBody["To"])
	require.Equal(t, "login@nightshift.test", gotBody["From"])
	require.Equal(t, "Sign in", gotBody["Subject"])
	require.Contains(t, gotBody["TextBody"], "token=abc")
}

func TestPostmarkErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"ErrorCode":300,"Message":"Invalid email"}`))
	}))
	defer srv.Close()

	p := mail.NewPostmark("server-token", "login@nightshift.test")
	p.BaseURL = srv.URL
	err := p.Send(context.Background(), "bad", "Sign in", "body")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "server-token")
}

func TestLogSenderNeverErrors(t *testing.T) {
	var s mail.Sender = mail.LogSender{}
	require.NoError(t, s.Send(context.Background(), "pat@acme.test", "Sign in", "body"))
}
