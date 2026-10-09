package logger

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/domain"
)

const (
	MaxCoreLogSize = 64 * 1024
	OnlyLogErrors  = true
)

type CoreLogger struct {
	writer    *RollingLogWriter
	lastError string
	level     slog.Level
	mu        sync.Mutex
}

func NewCoreLogger(baseDir string) *CoreLogger {
	logPath := filepath.Join(baseDir, domain.LogsDir, domain.CoreLogFile)
	return &CoreLogger{
		writer: NewRollingWriter(logPath, MaxCoreLogSize),
		level:  ParseLevel(domain.DefaultLogLevel),
	}
}

func (l *CoreLogger) SyncLogLevel(levelStr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = ParseLevel(levelStr)
}

func (l *CoreLogger) WriteLog(tag, rawMsg string) {
	level, cleanMsg := extractLevelAndMsg(rawMsg)

	if OnlyLogErrors {
		if level < slog.LevelError {
			return
		}
	} else if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastError == cleanMsg {
		return
	}
	l.lastError = cleanMsg

	timestamp := time.Now().Format(domain.TimeFormatLog)
	var entry string
	if tag != "" {
		entry = fmt.Sprintf("[%s] [%s] [%s] %s\n", timestamp, level.String(), tag, cleanMsg)
	} else {
		entry = fmt.Sprintf("[%s] [%s] %s\n", timestamp, level.String(), cleanMsg)
	}

	_, _ = l.writer.Write([]byte(entry))
	_ = l.writer.Sync()
}

func (l *CoreLogger) Close() error {
	if l.writer != nil {
		return l.writer.Close()
	}
	return nil
}

func extractLevelAndMsg(raw string) (slog.Level, string) {
	raw = strings.TrimSpace(raw)
	idx := strings.Index(raw, "level=")
	if idx == -1 {
		return slog.LevelError, raw
	}

	after := raw[idx+len("level="):]
	lvlStr := after
	if end := strings.IndexAny(after, " \t\r\n"); end != -1 {
		lvlStr = after[:end]
	}

	cleanMsg := raw
	if msgIdx := strings.Index(raw, "msg="); msgIdx != -1 {
		cleanMsg = strings.Trim(strings.TrimSpace(raw[msgIdx+len("msg="):]), `"`)
	}

	return ParseLevel(lvlStr), cleanMsg
}
