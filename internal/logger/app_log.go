package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"mihomo-tray/internal/domain"
)

const (
	MaxAppLogSize = 1024 * 1024 // 1 MB 轮转上限
	sessionHeader = "\n────────────────────────── [ New Session ] ──────────────────────────\n"
	timeFormatApp = domain.TimeFormatLog + ".000"
)

var GlobalLogLevel = new(slog.LevelVar)

type sessionWriter struct {
	target io.Writer
	header []byte
	once   sync.Once
}

func (w *sessionWriter) Write(p []byte) (n int, err error) {
	w.once.Do(func() {
		_, _ = w.target.Write(w.header)
	})
	return w.target.Write(p)
}

type customConsoleHandler struct {
	w     io.Writer
	level *slog.LevelVar
	mu    sync.Mutex
}

func (h *customConsoleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *customConsoleHandler) Handle(ctx context.Context, r slog.Record) error {
	var buf bytes.Buffer
	
	buf.WriteString(fmt.Sprintf("[%s] [%s] %s", r.Time.Format(timeFormatApp), r.Level.String(), r.Message))

	if r.NumAttrs() > 0 {
		buf.WriteString(" |")
		r.Attrs(func(a slog.Attr) bool {
			buf.WriteString(fmt.Sprintf(" %s=%v", a.Key, a.Value.Any()))
			return true
		})
	}
	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}
func (h *customConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *customConsoleHandler) WithGroup(name string) slog.Handler      { return h }


func Init(baseDir string, isRestarting bool) *RollingLogWriter {
	logPath := filepath.Join(baseDir, domain.LogsDir, domain.AppLogFile)
	writer := NewRollingWriter(logPath, MaxAppLogSize)

	GlobalLogLevel.Set(slog.LevelError)

	var logTarget io.Writer = writer
	if !isRestarting {
		logTarget = &sessionWriter{
			target: writer,
			header: []byte(sessionHeader),
		}
	}

	handler := &customConsoleHandler{
		w:     logTarget,
		level: GlobalLogLevel,
	}

	slog.SetDefault(slog.New(handler))
	return writer
}

func ParseLevel(s string) slog.Level {
	s = strings.ToLower(strings.TrimSpace(s))
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
		return slog.LevelError
	}
}

func SyncLogLevel(levelStr string) {
	GlobalLogLevel.Set(ParseLevel(levelStr))
}
