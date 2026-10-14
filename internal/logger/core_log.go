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
	DedupeInterval = 5 * time.Second
)

type CoreLogger struct {
	writer        *RollingLogWriter
	lastError     string
	lastErrorTime time.Time
	mu            sync.Mutex
}

func NewCoreLogger(baseDir string) *CoreLogger {
	logPath := filepath.Join(baseDir, domain.LogsDir, domain.CoreLogFile)
	return &CoreLogger{
		writer: NewRollingWriter(logPath, MaxCoreLogSize),
	}
}

func (l *CoreLogger) WriteLog(rawMsg string) {
	level, cleanMsg := extractLevelAndMsg(rawMsg)
	if level < slog.LevelError {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if l.lastError == cleanMsg && now.Sub(l.lastErrorTime) < DedupeInterval {
		return
	}
	l.lastError = cleanMsg
	l.lastErrorTime = now

	timestamp := now.Format(domain.TimeFormatLog)
	entry := fmt.Sprintf("[%s] [ERROR] %s\n", timestamp, cleanMsg)

	_, _ = l.writer.Write([]byte(entry))
	_ = l.writer.Sync()
}

func (l *CoreLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
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

	cleanMsg := raw[idx:]
	if msgIdx := strings.Index(raw, "msg="); msgIdx != -1 {
		val := strings.TrimSpace(raw[msgIdx+len("msg="):])
		if strings.HasPrefix(val, `"`) {
			if endQuote := strings.Index(val[1:], `"`); endQuote != -1 {
				cleanMsg = val[1 : endQuote+1]
			} else {
				cleanMsg = strings.Trim(val, `"`)
			}
		} else {
			if end := strings.IndexAny(val, " \t\r\n"); end != -1 {
				cleanMsg = val[:end]
			} else {
				cleanMsg = val
			}
		}
	}

	return ParseLevel(lvlStr), cleanMsg
}
