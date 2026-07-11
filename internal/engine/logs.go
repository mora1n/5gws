package engine

import (
	"bytes"
	"sync"
)

type LogBuffer struct {
	mu     sync.RWMutex
	data   []byte
	limit  int
	start  int
	size   int
	notify chan struct{}
}

func NewLogBuffer(limit int) *LogBuffer {
	if limit <= 0 {
		limit = 1 << 20
	}
	return &LogBuffer{data: make([]byte, limit), limit: limit, notify: make(chan struct{}, 1)}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	written := len(p)
	b.mu.Lock()
	if len(p) >= b.limit {
		copy(b.data, p[len(p)-b.limit:])
		b.start = 0
		b.size = b.limit
	} else if len(p) > 0 {
		end := (b.start + b.size) % b.limit
		first := min(len(p), b.limit-end)
		copy(b.data[end:], p[:first])
		copy(b.data, p[first:])
		if overflow := b.size + len(p) - b.limit; overflow > 0 {
			b.start = (b.start + overflow) % b.limit
		}
		b.size = min(b.limit, b.size+len(p))
	}
	b.mu.Unlock()
	select {
	case b.notify <- struct{}{}:
	default:
	}
	return written, nil
}

func (b *LogBuffer) Tail(lines int) string {
	b.mu.RLock()
	data := make([]byte, b.size)
	first := min(b.size, b.limit-b.start)
	copy(data, b.data[b.start:b.start+first])
	copy(data[first:], b.data[:b.size-first])
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
