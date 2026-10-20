package logger

import "sync"

const DefaultTailBufferSize = 64 * 1024

type TailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func NewTailBuffer(size ...int) *TailBuffer {
	bufSize := DefaultTailBufferSize
	if len(size) > 0 && size[0] > 0 {
		bufSize = size[0]
	}
	return &TailBuffer{
		buf: make([]byte, 0, bufSize),
		max: bufSize,
	}
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	needed := len(t.buf) + len(p)
	if needed > t.max {
		overflow := needed - t.max
		if overflow >= len(t.buf) {
			t.buf = t.buf[:0]
			p = p[len(p)-t.max:]
		} else {
			copy(t.buf, t.buf[overflow:])
			t.buf = t.buf[:len(t.buf)-overflow]

			for len(t.buf) > 0 && (t.buf[0]&0xC0) == 0x80 {
				t.buf = t.buf[1:]
			}
		}
	}

	t.buf = append(t.buf, p...)
	return len(p), nil
}

func (t *TailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func (t *TailBuffer) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = t.buf[:0]
}
