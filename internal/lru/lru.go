package lru

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"math/rand"

	"golang.org/x/exp/slices"
)

// newRand returns a new *math/rand.Rand object initialized with a seed from /dev/urandom.
func newRand() *rand.Rand {
	seedBytes := make([]byte, 8)
	if _, err := crand.Read(seedBytes); err != nil {
		panic(err)
	}
	seed := binary.LittleEndian.Uint64(seedBytes)

	return rand.New(rand.NewSource(int64(seed)))
}

// EvictCallback is used to get a callback when a cache entry is evicted
type EvictCallback[K comparable, V any] func(key K, value V)

// LRUStructSize is the size of the LRU struct -- there is a unit test to ensure
// this const matches the size measured with `unsafe.Sizeof`.
// TODO: move this to a file that is built only on 64-bit architectures and
// calculate the right size for 32-byte architectures
const LRUStructSize = 104

// LRU implements a non-thread safe, fixed size, approximate LRU cache.  Rather
// than a linked list encoding a strict LRU relationship, we approximate it by
// comparing 8 random entries and evicting the oldest.
type LRU[K comparable, V any] struct {
	items   map[K]int
	data    []entry[K, V]
	counter int64
	size    int64
	rng     rand.Rand
	onEvict EvictCallback[K, V]
}

// randomProbes is the number of elements we consider for eviction at a time,
// the oldest of which is evicted.
const randomProbes = 8

// entry is used to hold a value in the evictList
type entry[K comparable, V any] struct {
	lastUsed int64
	key      K
	value    V
}

// NewLRU constructs an LRU of the given size.  Memory for the full capacity of the
// LRU cache is allocated upfront.
func NewLRU[K comparable, V any](size int, onEvict EvictCallback[K, V]) (*LRU[K, V], error) {
	if size <= 0 {
		return nil, errors.New("must provide a positive size")
	}
	c := &LRU[K, V]{
		data:    make([]entry[K, V], 0, size),
		items:   make(map[K]int, size),
		counter: 1,
		size:    int64(size),
		rng:     *newRand(),
		onEvict: onEvict,
	}
	return c, nil
}

func (c *LRU[K, V]) getCounter() int64 {
	// if someone initializes a LRU as `&simplelru.LRU` directly, c.counter will
	// be initialized to zero.  increment it to 1 to avoid Problems (we use 0 as
	// a sentinel to mean "entry is not set") -- this branch will almost always be
	// predicted correctly, so this correctness fix should be costless.
	if c.counter == 0 {
		c.counter = 1
	}
	n := c.counter
	c.counter++
	return n
}

// Purge is used to completely clear the cache.
func (c *LRU[K, V]) Purge() {
	// only iterate through the items if we have an eviction callback registered.
	if c.onEvict != nil {
		for k, i := range c.items {
			if entry := &c.data[i]; entry.lastUsed > 0 {
				c.onEvict(k, entry.value)
			}
		}
	}

	c.data = c.data[:0]
	c.items = make(map[K]int, c.size)
}

//go:noinline
func (c *LRU[K, V]) shuffle() {
	c.rng.Shuffle(len(c.data), c.swap)
}

// Add adds a value to the cache.  Returns true if an eviction occurred.
func (c *LRU[K, V]) Add(key K, value V) (evicted bool) {
	now := c.getCounter()
	// Check for existing item
	if i, ok := c.items[key]; ok {
		entry := &c.data[i]
		entry.lastUsed = now
		entry.value = value
		return false
	}

	// if we were asked to be a zero-sized cache, return early
	if c.size == 0 {
		return
	}

	// Add new item
	ent := entry[K, V]{now, key, value}

	if int64(len(c.data)) == c.size {
		evicted = true
		if i, ok := c.findOldest(); ok {
			c.removeElement(i, c.data[i], false)
			c.data[i] = ent
			c.items[ent.key] = i
		} else {
			panic("invariant broken")
		}
		return
	}

	c.addShuffled(ent)

	return
}

// invarant: must have space in the array
func (c *LRU[K, V]) addShuffled(ent entry[K, V]) {
	if int64(len(c.data)) == c.size {
		panic("invariant broken")
	}

	i := len(c.data)

	c.data = append(c.data, ent)
	c.items[ent.key] = i

	j := c.rng.Intn(len(c.data))
	c.swap(i, j)
}

func (c *LRU[K, V]) swap(i, j int) {
	// nothing to do; don't touch memory
	if i == j {
		return
	}

	c.items[c.data[i].key] = j
	c.items[c.data[j].key] = i

	c.data[i], c.data[j] = c.data[j], c.data[i]
}

// Get looks up a key's value from the cache.
func (c *LRU[K, V]) Get(key K) (value V, ok bool) {
	if i, ok := c.items[key]; ok {
		entry := &c.data[i]
		// should never happen, but the check is cheap.
		if entry.key != key {
			var d V
			return d, false
		}
		entry.lastUsed = c.getCounter()
		return entry.value, true
	}
	return
}

// Contains checks if a key is in the cache, without updating the recent-ness
// or deleting it for being stale.
func (c *LRU[K, V]) Contains(key K) (ok bool) {
	_, ok = c.items[key]
	return ok
}

// Peek returns the key value (or undefined if not found) without updating
// the "recently used"-ness of the key.
func (c *LRU[K, V]) Peek(key K) (value V, ok bool) {
	if i, ok := c.items[key]; ok {
		return c.data[i].value, true
	}
	return value, false
}

// Remove removes the provided key from the cache, returning if the
// key was contained.
func (c *LRU[K, V]) Remove(key K) (present bool) {
	if i, ok := c.items[key]; ok {
		c.removeElement(i, c.data[i], true)
		return true
	}
	return false
}

// Len returns the number of items in the cache.
func (c *LRU[K, V]) Len() int {
	return len(c.items)
}

// Resize changes the cache size -- it is O(n * log(n)) expensive, and is best avoided.
func (c *LRU[K, V]) Resize(size int) (evicted int) {
	diff := c.Len() - size
	if diff < 0 {
		diff = 0
	}

	// sort in descending order, and update the items map to point at the
	// updated entry indexes
	slices.SortFunc(c.data, func(a, b entry[K, V]) bool {
		return a.lastUsed > b.lastUsed
	})

	for i, entry := range c.data {
		// if lastUsed is zero, the entry is actually empty/not-set.
		if entry.lastUsed == 0 {
			continue
		}
		c.items[entry.key] = i
	}

	// we may be downsizing the cache -- remove the oldest entries if so.
	oldSize := len(c.data)
	for i := 0; i < diff; i++ {
		j := oldSize - 1 - i
		c.removeElement(j, c.data[j], true)
	}

	c.size = int64(size)
	if size < oldSize {
		c.data = c.data[:size]
	} else {
		oldData := c.data
		c.data = make([]entry[K, V], oldSize, size)
		copy(c.data, oldData)
	}

	return diff
}

// findOldest identifies an old item from the cache (approximately _the_ oldest).
func (c *LRU[K, V]) findOldest() (off int, ok bool) {
	size := c.Len()
	if size <= 0 {
		return -1, false
	}

	// pick a random offset in our array of items to probe
	base := c.rng.Intn(size)
	oldestOff := base
	// _copy_ the initial oldest onto the stack
	var oldest entry[K, V] = c.data[base]

	// if our offset does NOT result in us wrapping off the end of the array
	// (which is very likely AND should be predicted well), don't require `% size`
	// inside the loop body, as that is expensive.  duplicate the whole loop to
	// put the conditional outside the loop rather than in it.
	if base+randomProbes-1 < size {
		for j := 1; j < randomProbes; j++ {
			off := base + j
			candidate := &c.data[off]
			if candidate.lastUsed < oldest.lastUsed {
				oldestOff = off
				oldest = *candidate
			}
		}
	} else {
		for j := 1; j < randomProbes; j++ {
			off := (base + j) % size
			candidate := &c.data[off]
			if candidate.lastUsed < oldest.lastUsed {
				oldestOff = off
				oldest = *candidate
			}
		}
	}

	return oldestOff, true
}

// removeElement is used to remove a given list element from the cache
func (c *LRU[K, V]) removeElement(i int, ent entry[K, V], doSwap bool) {
	if int64(i) >= c.size || len(c.data) == 0 {
		panic("invariant broken")
	}

	if doSwap {
		c.swap(i, len(c.data)-1)

		// clear out the item to avoid holding on to a reference for the GC
		c.data[len(c.data)-1] = entry[K, V]{}
		// truncate the array by 1
		c.data = c.data[:len(c.data)-1]
	}

	delete(c.items, ent.key)

	if c.onEvict != nil {
		c.onEvict(ent.key, ent.value)
	}
}
