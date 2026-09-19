package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	MaxLogFileSize = 25 * 1024 // 核心日志最大体积
	LogRetainSize  = 5 * 1024  // 触发轮转后保留的最新日志体积
)

type TailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func NewTailBuffer(maxSize int) *TailBuffer {
	return &TailBuffer{max: maxSize}
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		newBuf := make([]byte, t.max)
		copy(newBuf, t.buf[len(t.buf)-t.max:])
		t.buf = newBuf
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
	logDir := filepath.Join(baseDir, "logs")
	_ = os.MkdirAll(logDir, 0755)
	return &CoreLogger{logDir: logDir}
}

func (l *CoreLogger) WriteLog(errType, rawMsg string) {
	cleanedMsg := rawMsg
	if idx := strings.Index(rawMsg, "level="); idx != -1 {
		cleanedMsg = rawMsg[idx:]
	}

	l.mu.Lock()
	if l.lastError == cleanedMsg {
		l.mu.Unlock()
		return
	}
	l.lastError = cleanedMsg
	l.mu.Unlock()

	logPath := filepath.Join(l.logDir, "core.log")
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	finalLog := fmt.Sprintf("[%s] [%s] %s\n----------------------------------------\n", timestamp, errType, rawMsg)

	fi, err := os.Stat(logPath)
	if err == nil && fi.Size()+int64(len(finalLog)) > MaxLogFileSize {
		var keepData []byte
		f, err := os.Open(logPath)
		if err == nil {
			offset := fi.Size() - LogRetainSize
			if offset < 0 {
				offset = 0
			}
			keepData = make([]byte, fi.Size()-offset)
			_, _ = f.ReadAt(keepData, offset)
			f.Close()

			if offset > 0 {
				if idx := bytes.IndexByte(keepData, '\n'); idx != -1 {
					keepData = keepData[idx+1:]
				}
			}
		}

		notice := fmt.Sprintf("[%s] --- 日志大小已超限，仅保留最新部分 ---\n...\n", timestamp)
		combined := append(append([]byte(notice), keepData...), []byte(finalLog)...)
		_ = os.WriteFile(logPath, combined, 0644)
		return
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(finalLog)
}
