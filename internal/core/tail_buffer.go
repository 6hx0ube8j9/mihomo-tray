package core

import "sync"

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTailBuffer(maxSize int) *tailBuffer {
	return &tailBuffer{
		buf: make([]byte, 0, maxSize),
		max: maxSize,
	}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
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

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
