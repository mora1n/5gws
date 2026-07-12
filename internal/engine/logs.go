package engine

import (
	"bytes"
	"sync"
)

type LogBuffer struct {
	mu       sync.RWMutex
	data     []byte
	limit    int
	start    int
	size     int
	sequence uint64
	nextSub  uint64
	subs     map[uint64]chan uint64
}

func NewLogBuffer(limit int) *LogBuffer {
	if limit <= 0 {
		limit = 1 << 20
	}
	return &LogBuffer{data: make([]byte, limit), limit: limit, subs: make(map[uint64]chan uint64)}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if len(p) == 0 {
		return 0, nil
	}
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
	b.sequence++
	sequence := b.sequence
	for _, subscriber := range b.subs {
		select {
		case subscriber <- sequence:
		default:
		}
	}
	b.mu.Unlock()
	return written, nil
}

func (b *LogBuffer) Tail(lines int) string {
	text, _ := b.Snapshot(lines)
	return text
}

func (b *LogBuffer) Snapshot(lines int) (string, uint64) {
	b.mu.RLock()
	data := make([]byte, b.size)
	first := min(b.size, b.limit-b.start)
	copy(data, b.data[b.start:b.start+first])
	copy(data[first:], b.data[:b.size-first])
	sequence := b.sequence
	b.mu.RUnlock()
	if lines <= 0 {
		return string(data), sequence
	}
	for i, count := len(data)-1, 0; i >= 0; i-- {
		if data[i] != '\n' {
			continue
		}
		count++
		if count > lines {
			return string(bytes.TrimLeft(data[i+1:], "\n")), sequence
		}
	}
	return string(data), sequence
}

func (b *LogBuffer) Subscribe() (<-chan uint64, func()) {
	b.mu.Lock()
	b.nextSub++
	id := b.nextSub
	updates := make(chan uint64, 1)
	b.subs[id] = updates
	b.mu.Unlock()
	var once sync.Once
	return updates, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			close(updates)
			b.mu.Unlock()
		})
	}
}
