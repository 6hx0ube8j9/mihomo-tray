package logger

import (
	"log/slog"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
)

const MaxAppLogSize = 1024 * 1024 // 1 MB 轮转上限

var GlobalLogLevel = new(slog.LevelVar)

func Init(baseDir string) *RollingLogWriter {
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
	logger := slog.New(slog.NewTextHandler(writer, opts))
	slog.SetDefault(logger)
	return writer
}

func SyncLogLevel(levelStr string) {
	switch strings.ToLower(levelStr) {
	case "silent":
		GlobalLogLevel.Set(slog.Level(100))
	case "debug":
		GlobalLogLevel.Set(slog.LevelDebug)
	case "info":
		GlobalLogLevel.Set(slog.LevelInfo)
	case "warn":
		GlobalLogLevel.Set(slog.LevelWarn)
	case "error":
		GlobalLogLevel.Set(slog.LevelError)
	default:
		GlobalLogLevel.Set(slog.LevelError)
	}
}
