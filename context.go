package pocketflow

import (
	"context"
	"sync"
	"time"
)

type sharedStore struct {
	mu       sync.RWMutex
	values   map[any]any
	revision uint64
}

func (s *sharedStore) lookup(key any) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, exists := s.values[key]
	return value, exists
}

// PfContext combines cancellation, synchronized shared values, and run-local
// metadata. Do not copy it by value. Use WithParam, NewContext or WithContext.
type PfContext struct {
	context.Context
	storeOnce sync.Once
	store     *sharedStore
	params    map[string]any
	retry     int
}

// WithParam creates a shared store from a shallow copy of initial values.
func WithParam(parent context.Context, values map[any]any) *PfContext {
	if parent == nil {
		parent = context.Background()
	}
	initial := make(map[any]any, len(values))
	for key, value := range values {
		initial[key] = value
	}
	return &PfContext{Context: parent, store: &sharedStore{values: initial}}
}

func NewContext(parent context.Context) *PfContext { return WithParam(parent, nil) }

func (c *PfContext) shared() *sharedStore {
	c.storeOnce.Do(func() {
		if c.store == nil {
			c.store = &sharedStore{values: make(map[any]any)}
		}
	})
	return c.store
}

func (c *PfContext) parent() context.Context {
	if c == nil || c.Context == nil {
		return context.Background()
	}
	return c.Context
}

func (c *PfContext) Deadline() (time.Time, bool) { return c.parent().Deadline() }
func (c *PfContext) Done() <-chan struct{}       { return c.parent().Done() }
func (c *PfContext) Err() error                  { return c.parent().Err() }

// Value reads a shared value, falling back to the parent context when absent.
func (c *PfContext) Value(key any) any {
	value, exists := c.shared().lookup(key)
	if exists {
		return value
	}
	return c.parent().Value(key)
}

func (c *PfContext) SetValue(key, value any) {
	store := c.shared()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[key] = value
	store.revision++
}

// Update atomically reads, changes and stores a value. The callback must not
// call Value, SetValue, Update, Delete or Snapshot on this same shared store.
func (c *PfContext) Update(key any, update func(any) any) any {
	store := c.shared()
	var fallback any
	var revision uint64
	resolved := false
	for {
		value, updated := func() (any, bool) {
			store.mu.Lock()
			defer store.mu.Unlock()
			current, exists := store.values[key]
			if !exists {
				if !resolved || store.revision != revision {
					revision = store.revision
					return nil, false
				}
				current = fallback
			}
			next := update(current)
			store.values[key] = next
			store.revision++
			return next, true
		}()
		if updated {
			return value
		}
		// Parent contexts may wrap this same store. Resolve outside the lock,
		// then retry if a mutation could have made the inherited value stale.
		// Only the lookup is retried; the application callback runs once.
		fallback = c.parent().Value(key)
		resolved = true
	}
}

func (c *PfContext) Delete(key any) {
	store := c.shared()
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.values, key)
	store.revision++
}

// Snapshot returns a shallow copy. Synchronize mutation of nested values too.
func (c *PfContext) Snapshot() map[any]any {
	store := c.shared()
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make(map[any]any, len(store.values))
	for key, value := range store.values {
		result[key] = value
	}
	return result
}

// WithContext shares business values with a different cancellation context.
func (c *PfContext) WithContext(parent context.Context) *PfContext {
	if parent == nil {
		parent = context.Background()
	}
	return &PfContext{Context: parent, store: c.shared(), params: cloneParams(c.params), retry: c.retry}
}

// Params returns a shallow copy of the current execution's params.
func (c *PfContext) Params() map[string]any { return cloneParams(c.params) }

// RetryIndex is the zero-based exec attempt, including in fallback callbacks.
func (c *PfContext) RetryIndex() int { return c.retry }

func (c *PfContext) withExecution(params map[string]any) *PfContext {
	if c == nil {
		c = NewContext(context.Background())
	}
	return &PfContext{Context: c.parent(), store: c.shared(), params: params}
}

func (c *PfContext) withRetry(index int) *PfContext {
	return &PfContext{Context: c.parent(), store: c.shared(), params: c.params, retry: index}
}
