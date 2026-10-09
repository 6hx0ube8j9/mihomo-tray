package logger

import (
	"log/slog"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
)

const MaxAppLogSize = 1024 * 1024 // 1 MB

var GlobalLogLevel = new(slog.LevelVar)

func InitApp(baseDir string) *RollingLogWriter {
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
	slog.SetDefault(slog.New(slog.NewTextHandler(writer, opts)))
	return writer
}

func ParseLevel(s string) slog.Level {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "debug"):
		return slog.LevelDebug
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
	GlobalLogLevel.Set(ParseLevel(levelStr))
}
