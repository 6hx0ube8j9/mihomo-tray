package logger

import "sync"

const DefaultTailBufferSize = 64 * 1024

func NewTailBuffer(size ...int) *TailBuffer {
	bufSize := DefaultTailBufferSize
	if len(size) > 0 && size[0] > 0 {
		bufSize = size[0]
	}
	return &TailBuffer{
		maxSize: bufSize,
	}
}

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
