package klocker

import (
	"sync"
	"sync/atomic"
	"time"
)

const defaultCleanInterval = 30 * time.Minute

// Option defines a function type for modifying KLocker options.
type Option func(*KLocker)

// lockItem represents the lock data for each key, including the Mutex and reference count.
type lockItem struct {
	mutex *sync.Mutex
	count int32
}

// KLocker provides locking for keys with support for automatic cleanup of unused locks.
type KLocker struct {
	locks     sync.Map      // Map to store locks for each key
	cleanKeys sync.Map      // Map to track keys that need to be cleaned up
	closeCh   chan struct{} // Channel for stopping the cleanup goroutine
	interval  time.Duration // Interval for automatic cleanup
}

// WithCleanInterval is an option that sets the cleanup interval.
func WithCleanInterval(interval time.Duration) Option {
	return func(gl *KLocker) {
		if interval > 0 {
			gl.interval = interval
		}
	}
}

// New creates a new KLocker with provided options.
// It starts a background goroutine to periodically clean up unused locks.
func New(opts ...Option) *KLocker {
	gl := &KLocker{
		closeCh:  make(chan struct{}),
		interval: defaultCleanInterval, // Default cleanup interval
	}

	// Apply options to the KLocker
	for _, opt := range opts {
		opt(gl)
	}

	// Start the cleaner goroutine
	go gl.cleaner()

	return gl
}

// Lock acquires a lock for the given key. It increments the reference count and locks the mutex.
func (gl *KLocker) Lock(key string) {
	// Load the existing lock item or create a new one
	item, _ := gl.locks.LoadOrStore(key, &lockItem{
		mutex: &sync.Mutex{},
		count: 0,
	})

	lockData := item.(*lockItem)
	// Increment the reference count atomically
	atomic.AddInt32(&lockData.count, 1)
	// Lock the mutex
	lockData.mutex.Lock()
}

// Unlock releases the lock for the given key. It decrements the reference count.
// If no references remain, it marks the key for cleanup.
func (gl *KLocker) Unlock(key string) {
	if item, ok := gl.locks.Load(key); ok {
		lockData := item.(*lockItem)
		// Unlock the mutex
		lockData.mutex.Unlock()

		// Decrement the reference count atomically
		newCount := atomic.AddInt32(&lockData.count, -1)
		// If there are no more references, mark the key for cleanup
		if newCount <= 0 {
			gl.cleanKeys.Store(key, struct{}{})
		}
	}
}

// cleaner is a background goroutine that periodically runs cleanup tasks.
func (gl *KLocker) cleaner() {
	ticker := time.NewTicker(gl.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			gl.cleanup()
		case <-gl.closeCh:
			return
		}
	}
}

// cleanup removes the locks that are no longer in use.
func (gl *KLocker) cleanup() {
	var keysToRemove []string

	// Check each key marked for cleanup
	gl.cleanKeys.Range(func(key, _ interface{}) bool {
		k := key.(string)

		// If the lock is no longer in use, delete it
		if item, ok := gl.locks.Load(k); ok {
			lockData := item.(*lockItem)
			// If the reference count is 0 or less, the lock can be safely deleted
			if atomic.LoadInt32(&lockData.count) <= 0 {
				gl.locks.Delete(k)
				keysToRemove = append(keysToRemove, k)
			}
		}

		return true
	})

	// Clean up the keys from the clean-up tracking map
	for _, key := range keysToRemove {
		gl.cleanKeys.Delete(key)
	}
}

// Stop stops the cleaner goroutine.
func (gl *KLocker) Stop() {
	close(gl.closeCh)
}