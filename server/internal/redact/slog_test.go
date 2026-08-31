package redact_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/redact"
)

func TestHandlerRedactsMessageAndAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(redact.Handler{
		Inner: slog.NewTextHandler(&buf, nil),
		R:     redact.New([]string{"sk-super-secret"}),
	})
	logger.Error("upstream said sk-super-secret", "detail", "key=sk-super-secret rest")
	out := buf.String()
	require.NotContains(t, out, "sk-super-secret")
	require.True(t, strings.Contains(out, "[REDACTED]"))
}

func TestHandlerRedactsGroupedAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(redact.Handler{
		Inner: slog.NewTextHandler(&buf, nil),
		R:     redact.New([]string{"sk-super-secret"}),
	})
	logger.Error("upstream error",
		slog.Group("request", "authorization", "Bearer sk-super-secret"))
	out := buf.String()
	require.NotContains(t, out, "sk-super-secret")
	require.Contains(t, out, "[REDACTED]")
}
