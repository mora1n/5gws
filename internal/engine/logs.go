package engine

import (
	"bytes"
	"sync"
)

type LogBuffer struct {
	mu     sync.RWMutex
	data   []byte
	limit  int
	notify chan struct{}
}

func NewLogBuffer(limit int) *LogBuffer {
	if limit <= 0 {
		limit = 1 << 20
	}
	return &LogBuffer{limit: limit, notify: make(chan struct{}, 1)}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.data = append(b.data, p...)
	if extra := len(b.data) - b.limit; extra > 0 {
		copy(b.data, b.data[extra:])
		b.data = b.data[:b.limit]
	}
	b.mu.Unlock()
	select {
	case b.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (b *LogBuffer) Tail(lines int) string {
	b.mu.RLock()
	data := append([]byte(nil), b.data...)
	b.mu.RUnlock()
	if lines <= 0 {
		return string(data)
	}
	for i, count := len(data)-1, 0; i >= 0; i-- {
		if data[i] != '\n' {
			continue
		}
		count++
		if count > lines {
			return string(bytes.TrimLeft(data[i+1:], "\n"))
		}
	}
	return string(data)
}

func (b *LogBuffer) Notify() <-chan struct{} { return b.notify }
