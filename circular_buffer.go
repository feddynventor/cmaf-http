package main

import (
	"sync"
)

type CircularBuffer[T any] struct {
	data    []*T
	updates chan []*T
	mu      sync.Mutex
	size    int
	start   int
	count   int
	latest  *T
}

// initializes the buffer and starts the notification goroutine
func NewCircularBuffer[T any](size int) *CircularBuffer[T] {
	cb := &CircularBuffer[T]{
		data:    make([]*T, size),
		size:    size,
		updates: make(chan []*T),
	}
	return cb
}

// Add inserts a value into the buffer
func (cb *CircularBuffer[T]) Add(value *T) {
	cb.mu.Lock()

	// Overwrite the oldest value if the buffer is full
	if cb.count == cb.size {
		cb.data[cb.start] = nil
		cb.start = (cb.start + 1) % cb.size
	} else {
		cb.count++
	}

	// Add the value to the logical "end"
	cb.data[(cb.start+cb.count-1)%cb.size] = value
	cb.latest = value

	// Notify the notifier goroutine
	cb.mu.Unlock()
	cb.sendUpdate()
}

// retrieves the buffer contents in the correct order
func (cb *CircularBuffer[T]) Get() []*T {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	result := make([]*T, cb.count)
	for i := 0; i < cb.count; i++ {
		result[i] = cb.data[(cb.start+i)%cb.size]
	}
	return result
}

// provides read-only access to the channel
func (cb *CircularBuffer[T]) Updates() <-chan []*T {
	return cb.updates
}

// signals the notifier to send an update (decoupling the send from the Add method - nonblocking)
func (cb *CircularBuffer[T]) sendUpdate() {
	select {
	case cb.updates <- cb.Get(): // Send the buffer state
		// Successfully sent update
	default:
		// Drop the update if no listener is ready (prevents blocking)
	}
}
