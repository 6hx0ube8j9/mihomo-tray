package logger

import (
	"fmt"
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

func (l *CoreLogger) WriteLog(errType, rawMsg string) {
	cleanedMsg := rawMsg
	if idx := strings.Index(rawMsg, "level="); idx != -1 {
		cleanedMsg = rawMsg[idx:]
	}
	cleanedMsg = strings.TrimSpace(cleanedMsg)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastError == cleanedMsg {
		return
	}
	l.lastError = cleanedMsg

	timestamp := time.Now().Format(domain.TimeFormatLog)
	entry := fmt.Sprintf("[%s] [%s]\n%s\n----------------------------------------\n", timestamp, errType, cleanedMsg)

	_, _ = l.writer.Write([]byte(entry))
}

func (l *CoreLogger) Close() error {
	if l.writer != nil {
		return l.writer.Close()
	}
	return nil
}
