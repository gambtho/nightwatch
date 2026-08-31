package redact

import (
	"context"
	"log/slog"
)

// Handler wraps a slog.Handler, redacting known secret values from the
// message and every string attribute. Defense in depth: proxy and vault
// code never logs secrets on purpose; this catches accidents.
type Handler struct {
	Inner slog.Handler
	R     *Redactor
}

func (h Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.Inner.Enabled(ctx, l) }
func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{Inner: h.Inner.WithGroup(name), R: h.R}
}
func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return Handler{Inner: h.Inner.WithAttrs(h.redactAttrs(attrs)), R: h.R}
}

func (h Handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.R.Redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.Inner.Handle(ctx, out)
}

func (h Handler) redactAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = h.redactAttr(a)
	}
	return out
}

func (h Handler) redactAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.R.Redact(a.Value.String()))
	case slog.KindGroup:
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(h.redactAttrs(a.Value.Group())...)}
	default:
		return a
	}
}
