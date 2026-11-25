package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mihomo-tray/internal/domain"
)

const (
	MaxAppLogSize = 1024 * 1024
	sessionHeader = "[Startup]"
)

var GlobalLogLevel = new(slog.LevelVar)

type sessionWriter struct {
	target       *RollingLogWriter
	logPath      string
	isRestarting bool
	once         sync.Once
}

func (w *sessionWriter) Write(p []byte) (n int, err error) {
	w.once.Do(func() {
		if w.isRestarting {
			return
		}
		fi, statErr := os.Stat(w.logPath)
		isEmpty := statErr != nil || fi.Size() == 0

		if !isEmpty && fi.Size() < MaxAppLogSize {
			_, _ = w.target.Write([]byte("\r\n" + sessionHeader + "\r\n"))
		} else {
			_, _ = w.target.Write([]byte(sessionHeader + "\r\n"))
		}
	})

	n, err = w.target.Write(p)
	if err == nil {
		_ = w.target.Sync()
	}
	return n, err
}

type customConsoleHandler struct {
	w      io.Writer
	level  *slog.LevelVar
	attrs  []slog.Attr
	groups []string
}

func (h *customConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func cleanString(s string) string {
	if strings.ContainsAny(s, "\n\t\r") {
		s = strings.ReplaceAll(s, "\r\n", " ｜ ")
		s = strings.ReplaceAll(s, "\n", " ｜ ")
		s = strings.ReplaceAll(s, "\r", " ｜ ")
		s = strings.ReplaceAll(s, "\t", " ")
		s = strings.TrimSpace(s)
	}
	return s
}

func (h *customConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	buf.WriteString(fmt.Sprintf("%s %-5s %s", r.Time.Format(domain.TimeFormatLog), r.Level.String(), cleanString(r.Message)))

	var prefix string
	if len(h.groups) > 0 {
		prefix = strings.Join(h.groups, ".") + "."
	}

	appendAttr := func(a slog.Attr) {
		a.Value = a.Value.Resolve()
		if a.Equal(slog.Attr{}) {
			return
		}
		buf.WriteString(fmt.Sprintf(" %s%s=%s", prefix, a.Key, cleanString(fmt.Sprint(a.Value.Any()))))
	}

	for _, a := range h.attrs {
		appendAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(a)
		return true
	})

	buf.WriteString("\r\n")
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *customConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)

	return &customConsoleHandler{
		w:      h.w,
		level:  h.level,
		attrs:  newAttrs,
		groups: h.groups,
	}
}

func (h *customConsoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroups := make([]string, 0, len(h.groups)+1)
	newGroups = append(newGroups, h.groups...)
	newGroups = append(newGroups, name)

	return &customConsoleHandler{
		w:      h.w,
		level:  h.level,
		attrs:  h.attrs,
		groups: newGroups,
	}
}

func Init(baseDir string, isRestarting bool) *RollingLogWriter {
	logDir := filepath.Join(baseDir, domain.LogsDir)
	_ = os.MkdirAll(logDir, 0755)

	logPath := filepath.Join(logDir, domain.AppLogFile)
	writer := NewRollingWriter(logPath, MaxAppLogSize)

	GlobalLogLevel.Set(slog.LevelError)

	logTarget := &sessionWriter{
		target:       writer,
		logPath:      logPath,
		isRestarting: isRestarting,
	}

	handler := &customConsoleHandler{
		w:     logTarget,
		level: GlobalLogLevel,
	}

	slog.SetDefault(slog.New(handler))
	return writer
}

func ParseLevel(s string) slog.Level {
	s = strings.ToLower(strings.Trim(strings.TrimSpace(s), `"'`))
	switch {
	case strings.HasPrefix(s, "debug"):
		return slog.LevelDebug
	case strings.HasPrefix(s, "info"):
		return slog.LevelInfo
	case strings.HasPrefix(s, "warn"):
		return slog.LevelWarn
	case strings.HasPrefix(s, "error"), strings.HasPrefix(s, "fatal"), strings.HasPrefix(s, "panic"):
		return slog.LevelError
	case s == "silent":
		return slog.Level(100)
	default:
		return slog.LevelInfo
	}
}

func SyncLogLevel(levelStr string) {
	if strings.TrimSpace(levelStr) == "" {
		levelStr = domain.DefaultTrayLogLevel
	}
	GlobalLogLevel.Set(ParseLevel(levelStr))
}
