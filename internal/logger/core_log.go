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

const MaxCoreLogSize = 64 * 1024

type CoreLogger struct {
	writer    *RollingLogWriter
	lastError string
	mu        sync.Mutex
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

	if l.lastError == cleanMsg {
		return
	}
	l.lastError = cleanMsg

	timestamp := time.Now().Format(domain.TimeFormatLog)
	entry := fmt.Sprintf("[%s] [ERROR] %s\n", timestamp, cleanMsg)

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
