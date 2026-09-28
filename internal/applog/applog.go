package applog

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const MaxLogSize = 1024 * 1024

var GlobalLogLevel = new(slog.LevelVar)

type RollingLogWriter struct {
	mu       sync.Mutex
	logPath  string
	bakPath  string
	file     *os.File
	currSize int64
}

func (w *RollingLogWriter) open() {
	_ = os.MkdirAll(filepath.Dir(w.logPath), 0755)

	fi, err := os.Stat(w.logPath)
	if err == nil {
		w.currSize = fi.Size()
		if w.currSize >= MaxLogSize {
			w.rotate()
			return
		}
	} else {
		w.currSize = 0
	}
	w.file, _ = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
}

func (w *RollingLogWriter) rotate() {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	_ = os.Remove(w.bakPath)
	renameErr := os.Rename(w.logPath, w.bakPath)
	var file *os.File
	var err error
	if renameErr == nil {
		file, err = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	} else {
		file, err = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	}
	if err == nil {
		w.file = file
		w.currSize = 0
	}
}

func (w *RollingLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		w.open()
		if w.file == nil {
			return len(p), nil
		}
	}
	if w.currSize+int64(len(p)) > MaxLogSize {
		w.rotate()
		if w.file == nil {
			return len(p), nil
		}
	}
	n, err = w.file.Write(p)
	if err == nil {
		w.currSize += int64(n)
	}
	return n, err
}

func (w *RollingLogWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
}

func Init(baseDir string) *RollingLogWriter {
	logDir := filepath.Join(baseDir, "logs")
	writer := &RollingLogWriter{
		logPath: filepath.Join(logDir, "mihomo-tray.log"),
		bakPath: filepath.Join(logDir, "mihomo-tray.log.bak"),
	}

	GlobalLogLevel.Set(slog.LevelError)
	opts := &slog.HandlerOptions{
		Level: GlobalLogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				t := a.Value.Time()
				a.Value = slog.StringValue(t.Format("2006/01/02 15:04:05"))
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
