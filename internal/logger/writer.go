package logger

import (
	"os"
	"path/filepath"
	"sync"

	"mihomo-tray/internal/fs"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type RollingLogWriter struct {
	mu       sync.Mutex
	logPath  string
	bakPath  string
	maxSize  int64
	file     *os.File
	currSize int64
}

func NewRollingWriter(logPath string, maxSize int64) *RollingLogWriter {
	return &RollingLogWriter{
		logPath: logPath,
		bakPath: logPath + ".bak",
		maxSize: maxSize,
	}
}

func (w *RollingLogWriter) open() {
	_ = os.MkdirAll(filepath.Dir(w.logPath), 0755)

	fi, err := os.Stat(w.logPath)
	if err == nil {
		w.currSize = fi.Size()
		if w.currSize >= w.maxSize {
			w.rotate()
			return
		}
	} else {
		w.currSize = 0
	}

	w.file, _ = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if w.file != nil && w.currSize == 0 {
		n, _ := w.file.Write(utf8BOM)
		w.currSize += int64(n)
	}
}

func (w *RollingLogWriter) rotate() {
	if w.file != nil {
		_ = w.file.Sync()
		_ = w.file.Close()
		w.file = nil
	}

	err := fs.ReplaceAtomic(w.logPath, w.bakPath)

	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	w.file, _ = os.OpenFile(w.logPath, flags, 0666)
	w.currSize = 0

	if w.file != nil {
		n, _ := w.file.Write(utf8BOM)
		w.currSize += int64(n)

		if err != nil {
			msg := []byte("[Warning] Log rotation failed (file locked). Old logs were truncated to prevent I/O deadlock.\r\n")
			wn, _ := w.file.Write(msg)
			w.currSize += int64(wn)
		}
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

	if w.currSize+int64(len(p)) > w.maxSize {
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

func (w *RollingLogWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

func (w *RollingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_ = w.file.Sync()
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}
