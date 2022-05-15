package approxlru

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"math/rand"
	"sort"
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
type EvictCallback func(key string, value interface{})

// LRUStructSize is the size of the LRU struct -- there is a unit test to ensure
// this const matches the size measured with `unsafe.Sizeof`.
// TODO: move this to a file that is built only on 64-bit architectures and
// calculate the right size for 32-byte architectures
const LRUStructSize = 104

// LRU implements a non-thread safe, fixed size, approximate LRU cache.  Rather
// than a linked list encoding a strict LRU relationship, we approximate it by
// comparing 8 random entries and evicting the oldest.
type LRU struct {
	items   map[string]int
	data    []entry
	counter int64
	size    int64
	rng     rand.Rand
	onEvict EvictCallback
}

// randomProbes is the number of elements we consider for eviction at a time,
// the oldest of which is evicted.
const randomProbes = 8

// entry is used to hold a value in the evictList
type entry struct {
	lastUsed int64
	key      string
	value    interface{}
}

// NewLRU constructs an LRU of the given size.  Memory for the full capacity of the
// LRU cache is allocated upfront.
func NewLRU(size int, onEvict EvictCallback) (*LRU, error) {
	if size <= 0 {
		return nil, errors.New("must provide a positive size")
	}
	c := &LRU{
		data:    make([]entry, 0, size),
		items:   make(map[string]int, size),
		counter: 1,
		size:    int64(size),
		rng:     *newRand(),
		onEvict: onEvict,
	}
	return c, nil
}

func (c *LRU) getCounter() int64 {
	// if someone initializes a LRU as `&simplelru.LRU` directly, c.counter will
	// be initialized to zero.  increment it to 1 to avoid Problems (we use 0 as
	// a sentinel to mean "entry is not set") -- this branch will almost always be
	// predicted correctly, so this correctness fix should be costless.
	if c.counter == 0 {
		c.counter = 1
	}
	n := c.counter
	c.counter++
	if c.counter < 0 {
		panic("counter overflow; won't happen in practice :rip:")
	}
	return n
}

// Purge is used to completely clear the cache.
func (c *LRU) Purge() {
	// only iterate through the items if we have an eviction callback registered.
	if c.onEvict != nil {
		for k, i := range c.items {
			if entry := &c.data[i]; entry.lastUsed > 0 {
				c.onEvict(k, entry.value)
			}
		}
	}

	c.data = c.data[:0]
	c.items = make(map[string]int, c.size)
}

//go:noinline
func (c *LRU) shuffle() {
	c.rng.Shuffle(len(c.data), func(i, j int) {
		c.items[c.data[i].key] = j
		c.items[c.data[j].key] = i

		c.data[i], c.data[j] = c.data[j], c.data[i]
	})
}

// Add adds a value to the cache.  Returns true if an eviction occurred.
func (c *LRU) Add(key string, value interface{}) (evicted bool) {
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
	ent := entry{now, key, value}

	if int64(len(c.data)) < c.size {
		i := len(c.data)
		c.data = append(c.data, ent)
		c.items[key] = i
		// if we have filled up the cache for the first time, shuffle
		// the items to ensure they are randomly distributed in the array.
		// we need this to ensure our random probing in removeOldest is correct.
		if int64(len(c.data)) == c.size {
			c.shuffle()
		}
	} else {
		evicted = true
		i := c.removeOldest()
		c.data[i] = ent
		c.items[key] = i
	}

	return
}

// Get looks up a key's value from the cache.
func (c *LRU) Get(key string) (value interface{}, ok bool) {
	if i, ok := c.items[key]; ok {
		entry := &c.data[i]
		// should never happen, but the check is cheap.
		if entry.key != key {
			return nil, false
		}
		entry.lastUsed = c.getCounter()
		return entry.value, true
	}
	return
}

// Contains checks if a key is in the cache, without updating the recent-ness
// or deleting it for being stale.
func (c *LRU) Contains(key string) (ok bool) {
	_, ok = c.items[key]
	return ok
}

// Peek returns the key value (or undefined if not found) without updating
// the "recently used"-ness of the key.
func (c *LRU) Peek(key string) (value interface{}, ok bool) {
	if i, ok := c.items[key]; ok {
		return c.data[i].value, true
	}
	return value, false
}

// Remove removes the provided key from the cache, returning if the
// key was contained.
func (c *LRU) Remove(key string) (present bool) {
	if i, ok := c.items[key]; ok {
		c.removeElement(i, c.data[i])
		return true
	}
	return false
}

// Len returns the number of items in the cache.
func (c *LRU) Len() int {
	return len(c.items)
}

// byLastUsed is used to sort a slice of entry structs by its lastUsed value
// in descending order.
type byLastUsed []entry

func (a byLastUsed) Len() int           { return len(a) }
func (a byLastUsed) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byLastUsed) Less(i, j int) bool { return a[i].lastUsed > a[j].lastUsed }

// Resize changes the cache size -- it is O(n * log(n)) expensive, and is best avoided.
func (c *LRU) Resize(size int) (evicted int) {
	diff := c.Len() - size
	if diff < 0 {
		diff = 0
	}

	// sort in descending order, and update the items map to point at the
	// updated entry indexes
	sort.Sort(byLastUsed(c.data))
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
		c.removeElement(j, c.data[j])
	}

	c.size = int64(size)
	if size < oldSize {
		c.data = c.data[:size]
	} else {
		oldData := c.data
		c.data = make([]entry, oldSize, size)
		copy(c.data, oldData)
	}

	// now that we've resized, shuffle things so that random probing works.
	c.shuffle()

	return diff
}

// removeOldest removes an old item from the cache (approximately _the_ oldest).
func (c *LRU) removeOldest() (off int) {
	size := c.Len()
	if size <= 0 {
		return -1
	}

	// pick a random offset in our array of items to probe
	base := c.rng.Intn(size)
	oldestOff := base
	// _copy_ the initial oldest onto the stack
	var oldest entry = c.data[base]

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

	c.removeElement(oldestOff, oldest)

	return oldestOff
}

// removeElement is used to remove a given list element from the cache
func (c *LRU) removeElement(i int, ent entry) {
	// we could have found an empty slot -- nothing to remove if that is the case.
	if ent.lastUsed == 0 {
		return
	}

	c.data[i] = entry{}
	delete(c.items, ent.key)
	if c.onEvict != nil {
		c.onEvict(ent.key, ent.value)
	}
}
