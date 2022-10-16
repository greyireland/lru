package lru

import (
	"sync"

	"github.com/greyireland/lru/internal/lru"
)

// Cache is a thread-safe fixed size LRU cache.
type Cache[K comparable, V any] struct {
	lock sync.Mutex
	lru  lru.LRU[K, V]
	_    [16]byte
}

// New creates an LRU of the given size.
func New[K comparable, V any](size int) *Cache[K, V] {
	return NewWithEvict[K, V](size, nil)
}

// NewWithEvict constructs a fixed size cache with the given eviction
// callback.
func NewWithEvict[K comparable, V any](size int, onEvicted func(key K, value V)) *Cache[K, V] {
	lru, err := lru.NewLRU(size, onEvicted)
	if err != nil {
		panic(err)
	}
	c := &Cache[K, V]{
		lru: *lru,
	}
	return c
}

// Purge is used to completely clear the cache.
func (c *Cache[K, V]) Purge() {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.lru.Purge()
}

// Add adds a value to the cache. Returns true if an eviction occurred.
func (c *Cache[K, V]) Add(key K, value V) (evicted bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Add(key, value)
}

// Get looks up a key's value from the cache.
func (c *Cache[K, V]) Get(key K) (value V, ok bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Get(key)
}

// Contains checks if a key is in the cache, without updating the
// recent-ness or deleting it for being stale.
func (c *Cache[K, V]) Contains(key K) bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Contains(key)
}

// Peek returns the key value (or undefined if not found) without updating
// the "recently used"-ness of the key.
func (c *Cache[K, V]) Peek(key K) (value V, ok bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Peek(key)
}

// ContainsOrAdd checks if a key is in the cache without updating the
// recent-ness or deleting it for being stale, and if not, adds the value.
// Returns whether found and whether an eviction occurred.
func (c *Cache[K, V]) ContainsOrAdd(key K, value V) (ok, evicted bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.lru.Contains(key) {
		return true, false
	}
	evicted = c.lru.Add(key, value)
	return false, evicted
}

// PeekOrAdd checks if a key is in the cache without updating the
// recent-ness or deleting it for being stale, and if not, adds the value.
// Returns whether found and whether an eviction occurred.
func (c *Cache[K, V]) PeekOrAdd(key K, value V) (previous V, ok, evicted bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	previous, ok = c.lru.Peek(key)
	if ok {
		return previous, true, false
	}

	evicted = c.lru.Add(key, value)
	return previous, false, evicted
}

// Remove removes the provided key from the cache.
func (c *Cache[K, V]) Remove(key K) (present bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Remove(key)
}

// Resize changes the cache size.
func (c *Cache[K, V]) Resize(size int) (evicted int) {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Resize(size)
}

// Len returns the number of items in the cache.
func (c *Cache[K, V]) Len() int {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Len()
}
