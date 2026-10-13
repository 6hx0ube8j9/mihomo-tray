package logger

import (
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

func Init(baseDir string, isRestarting bool) *RollingLogWriter {
	logPath := filepath.Join(baseDir, domain.LogsDir, domain.AppLogFile)
	writer := NewRollingWriter(logPath, MaxAppLogSize)

	GlobalLogLevel.Set(slog.LevelError)
	opts := &slog.HandlerOptions{
		Level: GlobalLogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format(domain.TimeFormatLog))
			}
			return a
		},
	}

	var logTarget io.Writer = writer
	if !isRestarting {
		logTarget = &sessionWriter{
			target: writer,
			header: []byte(sessionHeader),
		}
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(logTarget, opts)))
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
