package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

const (
	MaxLogFileSize = 25 * 1024 // 核心日志最大体积 (25KB)
	LogRetainSize  = 5 * 1024  // 保留历史日志体积 (5KB)
)

type TailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func NewTailBuffer(maxSize int) *TailBuffer {
	return &TailBuffer{
		buf: make([]byte, 0, maxSize),
		max: maxSize,
	}
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		overflow := len(t.buf) - t.max
		copy(t.buf, t.buf[overflow:])
		t.buf = t.buf[:t.max]
	}
	return len(p), nil
}

func (t *TailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

type CoreLogger struct {
	logDir    string
	lastError string
	mu        sync.Mutex
}

func NewCoreLogger(baseDir string) *CoreLogger {
	logDir := filepath.Join(baseDir, domain.LogsDir)
	_ = os.MkdirAll(logDir, 0755)
	return &CoreLogger{logDir: logDir}
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

	logPath := filepath.Join(l.logDir, domain.CoreLogFile)
	timestamp := time.Now().Format(domain.TimeFormatLog)
	entry := fmt.Sprintf("[%s] [%s]\n%s\n----------------------------------------\n", timestamp, errType, cleanedMsg)

	fi, err := os.Stat(logPath)
	if err == nil && fi.Size()+int64(len(entry)) > MaxLogFileSize {
		l.rotateLocked(logPath, entry, timestamp, fi.Size())
		return
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(entry)
}

func (l *CoreLogger) rotateLocked(logPath, newEntry, timestamp string, currSize int64) {
	var keepData []byte
	content, err := os.ReadFile(logPath)
	if err == nil && len(content) > 0 {
		offset := int(currSize) - LogRetainSize
		if offset < 0 {
			offset = 0
		}
		keepData = content[offset:]
		if idx := bytes.IndexByte(keepData, '\n'); idx != -1 {
			keepData = keepData[idx+1:]
		}
	}

	notice := fmt.Sprintf("[%s] --- 历史日志已自动清理 ---\n...\n", timestamp)
	combined := append([]byte(notice), keepData...)
	combined = append(combined, []byte(newEntry)...)

	_ = fs.WriteAtomic(logPath, combined)
}
