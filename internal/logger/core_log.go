package logger

import (
	"encoding/json"
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
	if level < slog.LevelError || cleanMsg == "" {
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
	entry := fmt.Sprintf("[%s] [ERROR] %s\r\n", timestamp, cleanMsg)

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
	if raw == "" {
		return slog.LevelInfo, ""
	}

	if strings.HasPrefix(raw, "{") && strings.Contains(raw, `"type"`) {
		var j struct {
			Type    string `json:"type"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal([]byte(raw), &j); err == nil && j.Type != "" {
			return ParseLevel(j.Type), strings.TrimSpace(j.Payload)
		}
	}

	if idx := strings.Index(raw, "level="); idx != -1 {
		after := raw[idx+len("level="):]
		lvlStr := after
		if end := strings.IndexAny(after, " \t\r\n"); end != -1 {
			lvlStr = after[:end]
		}

		cleanMsg := raw
		if msgIdx := strings.Index(raw, "msg="); msgIdx != -1 {
			val := strings.TrimSpace(raw[msgIdx+len("msg="):])
			cleanMsg = extractQuotedOrPlain(val)
		}
		return ParseLevel(lvlStr), strings.TrimSpace(cleanMsg)
	}

	upper := strings.ToUpper(raw)
	switch {
	case strings.Contains(upper, "[ERROR]"), strings.Contains(upper, "[FATAL]"),
		strings.HasPrefix(raw, "panic:"), strings.HasPrefix(raw, "fatal error:"):
		return slog.LevelError, raw
	case strings.Contains(upper, "[WARN]"):
		return slog.LevelWarn, raw
	case strings.Contains(upper, "[INFO]"):
		return slog.LevelInfo, raw
	case strings.Contains(upper, "[DEBUG]"):
		return slog.LevelDebug, raw
	}

	lower := strings.ToLower(raw)
	if strings.Contains(lower, "error") || strings.Contains(lower, "failed") {
		return slog.LevelError, raw
	}

	return slog.LevelInfo, raw
}

func extractQuotedOrPlain(val string) string {
	if !strings.HasPrefix(val, `"`) {
		if end := strings.IndexAny(val, " \t\r\n"); end != -1 {
			return val[:end]
		}
		return val
	}

	escaped := false
	for i := 1; i < len(val); i++ {
		c := val[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			return val[1:i]
		}
	}
	return strings.Trim(val, `"`)
}
