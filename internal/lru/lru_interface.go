// Package simplelru provides simple LRU implementation based on build-in container/list.
package lru

// LRUCache is the interface for simple LRU cache.
type LRUCache[K comparable, V any] interface {
	// Adds a value to the cache, returns true if an eviction occurred and
	// updates the "recently used"-ness of the key.
	Add(key K, value V) bool

	// Returns key's value from the cache and
	// updates the "recently used"-ness of the key. #value, isFound
	Get(key K) (value V, ok bool)

	// Checks if a key exists in cache without updating the recent-ness.
	Contains(key K) (ok bool)

	// Returns key's value without updating the "recently used"-ness of the key.
	Peek(key K) (value V, ok bool)

	// Removes a key from the cache.
	Remove(key K) bool

	// Returns the number of items in the cache.
	Len() int

	// Clears all cache entries.
	Purge()

	// Resizes cache, returning number evicted
	Resize(int) int
}
