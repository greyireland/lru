package lru

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"unsafe"
)

func FuzzCache(f *testing.F) {
	trace := make([]traceEntry, 36)
	for i := 0; i < 36; i++ {
		var n int64
		if i%2 == 0 {
			n = rand.Int63() % 16384
		} else {
			n = rand.Int63() % 32768
		}
		trace[i] = traceEntry{strconv.FormatInt(n, 10), n}
	}

	f.Add([]byte{0, 1})
	f.Fuzz(func(t *testing.T, s []byte) {
		l, err := New(32)
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		for i, b := range s {
			t := trace[i%36]
			// call add with 2/3 probability
			if b < 192 {
				l.Add(t.k, t.v)
			} else {
				l.Remove(t.k)
			}
		}
	})
}

func TestNonShardSize(t *testing.T) {
	size := unsafe.Sizeof(Cache{})
	if 128 != size {
		t.Fatalf("expected shard to be 128-bytes in size, not %d", size)
	}
}

func newRand() *rand.Rand {
	seedBytes := make([]byte, 8)
	if _, err := crand.Read(seedBytes); err != nil {
		panic(err)
	}
	seed := binary.LittleEndian.Uint64(seedBytes)

	return rand.New(rand.NewSource(int64(seed)))
}

type traceEntry struct {
	k string
	v int64
}

func BenchmarkLRU_Rand(b *testing.B) {
	l, err := New(8192)
	if err != nil {
		b.Fatalf("err: %v", err)
	}

	trace := make([]traceEntry, b.N*2)
	for i := 0; i < b.N*2; i++ {
		n := rand.Int63() % 32768
		trace[i] = traceEntry{strconv.FormatInt(n, 10), n}
	}

	b.ResetTimer()

	var hit, miss int
	for i := 0; i < 2*b.N; i++ {
		t := trace[i]
		if i%2 == 0 {
			l.Add(t.k, t.v)
		} else {
			_, ok := l.Get(t.k)
			if ok {
				hit++
			} else {
				miss++
			}
		}
	}
	b.Logf("hit: %d miss: %d ratio: %f", hit, miss, float64(hit)/float64(miss))
}

func BenchmarkLRU_Freq(b *testing.B) {
	l, err := New(8192)
	if err != nil {
		b.Fatalf("err: %v", err)
	}

	trace := make([]traceEntry, b.N*2)
	for i := 0; i < b.N*2; i++ {
		var n int64
		if i%2 == 0 {
			n = rand.Int63() % 16384
		} else {
			n = rand.Int63() % 32768
		}
		trace[i] = traceEntry{strconv.FormatInt(n, 10), n}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		t := trace[i]
		l.Add(t.k, t.v)
	}
	var hit, miss int
	for i := 0; i < b.N; i++ {
		_, ok := l.Get(trace[i].k)
		if ok {
			hit++
		} else {
			miss++
		}
	}
	b.Logf("hit: %d miss: %d ratio: %f", hit, miss, float64(hit)/float64(miss))
}

func BenchmarkLRU_Big(b *testing.B) {
	var rngMu sync.Mutex
	rng := newRand()
	rngMu.Lock()
	l, err := New(128 * 1024)
	if err != nil {
		b.Fatalf("err: %v", err)
	}

	type traceEntry struct {
		k string
		v int64
	}
	trace := make([]traceEntry, b.N*2)
	for i := 0; i < b.N*2; i++ {
		n := rng.Int63() % (4 * 128 * 1024)
		trace[i] = traceEntry{k: strconv.Itoa(int(n)), v: n}
	}
	rngMu.Unlock()

	b.ResetTimer()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		rngMu.Lock()
		seed := rng.Intn(len(trace))
		rngMu.Unlock()

		var hit, miss int
		i := seed
		for pb.Next() {
			// use a predictable if rather than % len(trace) to eek a little more perf out
			if i >= len(trace) {
				i = 0
			}

			t := trace[i]
			if i%2 == 0 {
				l.Add(t.k, t.v)
			} else {
				if _, ok := l.Get(t.k); ok {
					hit++
				} else {
					miss++
				}
			}

			i++
		}
		if hit > 1000 {
			b.Logf("hit: %d miss: %d ratio: %f", hit, miss, float64(hit)/float64(miss))
		}
	})
}

func TestLRU(t *testing.T) {
	evictCounter := 0
	onEvicted := func(k string, v interface{}) {
		if k != fmt.Sprintf("%v", v) {
			t.Fatalf("Evict values not equal (%v!=%v)", k, v)
		}
		evictCounter++
	}
	l, err := NewWithEvict(128, onEvicted)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	for i := 0; i < 256; i++ {
		l.Add(strconv.Itoa(i), i)
	}
	if l.Len() != 128 {
		t.Fatalf("bad len: %v", l.Len())
	}

	if evictCounter != 128 {
		t.Fatalf("bad evict count: %v", evictCounter)
	}

	stale := 0
	for i := 0; i < 128; i++ {
		_, ok := l.Get(strconv.Itoa(i))
		if ok {
			stale++
		}
	}
	// if we had a perfect LRU, this would be 0.  since we are approximating an LRU, this is slightly non-zero
	if stale > 20 {
		t.Fatalf("too many stale: %d", stale)
	}

	diedBeforeTheirTime := 0
	for i := 128; i < 256; i++ {
		_, ok := l.Get(strconv.Itoa(i))
		if !ok {
			diedBeforeTheirTime++
		}
	}
	// if we had a perfect LRU, this would be 0.  since we are approximating an LRU, this is slightly non-zero
	if diedBeforeTheirTime > 20 {
		t.Fatalf("too many 'new' evicted early: %d", diedBeforeTheirTime)
	}

	for i := 128; i < 192; i++ {
		k := strconv.Itoa(i)
		l.Remove(k)
		_, ok := l.Get(k)
		if ok {
			t.Fatalf("should be deleted")
		}
	}

	l.Get("192") // expect 192 to be last key in l.Keys()

	/*for i, k := range l.Keys() {
		if (i < 63 && k != i+193) || (i == 63 && k != 192) {
			t.Fatalf("out of order key: %v", k)
		}
	}*/

	l.Purge()
	if l.Len() != 0 {
		t.Fatalf("bad len: %v", l.Len())
	}
	if _, ok := l.Get("200"); ok {
		t.Fatalf("should contain nothing")
	}
}

// test that Add returns true/false if an eviction occurred
func TestLRUAdd(t *testing.T) {
	evictCounter := 0
	onEvicted := func(k string, v interface{}) {
		evictCounter++
	}

	l, err := NewWithEvict(1, onEvicted)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if l.Add("1", 1) == true || evictCounter != 0 {
		t.Errorf("should not have an eviction")
	}
	if l.Add("2", 2) == false || evictCounter != 1 {
		t.Errorf("should have an eviction")
	}
}

// test that Contains doesn't update recent-ness
func TestLRUContains(t *testing.T) {
	l, err := New(2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add("1", 1)
	l.Add("2", 2)
	if !l.Contains("1") {
		t.Errorf("1 should be contained")
	}

	l.Add("3", 3)
	if l.Contains("1") {
		t.Errorf("Contains should not have updated recent-ness of 1")
	}
}

// test that ContainsOrAdd doesn't update recent-ness
func TestLRUContainsOrAdd(t *testing.T) {
	l, err := New(2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add("1", 1)
	l.Add("2", 2)
	contains, evict := l.ContainsOrAdd("1", 1)
	if !contains {
		t.Errorf("1 should be contained")
	}
	if evict {
		t.Errorf("nothing should be evicted here")
	}

	l.Add("3", 3)
	contains, evict = l.ContainsOrAdd("1", 1)
	if contains {
		t.Errorf("1 should not have been contained")
	}
	if !evict {
		t.Errorf("an eviction should have occurred")
	}
	if !l.Contains("1") {
		t.Errorf("now 1 should be contained")
	}
}

// test that PeekOrAdd doesn't update recent-ness
func TestLRUPeekOrAdd(t *testing.T) {
	l, err := New(2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add("1", 1)
	l.Add("2", 2)
	previous, contains, evict := l.PeekOrAdd("1", 1)
	if !contains {
		t.Errorf("1 should be contained")
	}
	if evict {
		t.Errorf("nothing should be evicted here")
	}
	if previous != 1 {
		t.Errorf("previous is not equal to 1")
	}

	l.Add("3", 3)
	contains, evict = l.ContainsOrAdd("1", 1)
	if contains {
		t.Errorf("1 should not have been contained")
	}
	if !evict {
		t.Errorf("an eviction should have occurred")
	}
	if !l.Contains("1") {
		t.Errorf("now 1 should be contained")
	}
}

// test that Peek doesn't update recent-ness
func TestLRUPeek(t *testing.T) {
	l, err := New(2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add("1", 1)
	l.Add("2", 2)
	if v, ok := l.Peek("1"); !ok || v != 1 {
		t.Errorf("1 should be set to 1: %v, %v", v, ok)
	}

	l.Add("3", 3)
	if l.Contains("1") {
		t.Errorf("should not have updated recent-ness of 1")
	}
}

// test that Resize can upsize and downsize
func TestLRUResize(t *testing.T) {
	onEvictCounter := 0
	onEvicted := func(k string, v interface{}) {
		onEvictCounter++
	}
	l, err := NewWithEvict(2, onEvicted)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Downsize
	l.Add("1", 1)
	l.Add("2", 2)
	evicted := l.Resize(1)
	// no guarantees
	//if evicted != 1 {
	//	t.Errorf("1 element should have been evicted: %v", evicted)
	//}
	if onEvictCounter != 1 {
		t.Errorf("onEvicted should have been called 1 time: %v", onEvictCounter)
	}

	l.Add("3", 3)
	if l.Contains("1") {
		t.Errorf("Element 1 should have been evicted")
	}

	// Upsize
	evicted = l.Resize(2)
	if evicted != 0 {
		t.Errorf("0 elements should have been evicted: %v", evicted)
	}

	l.Add("4", 4)
	if !l.Contains("3") || !l.Contains("4") {
		t.Errorf("Cache should have contained 2 elements")
	}
}

func BenchmarkLRU_HotKey(b *testing.B) {
	var rngMu sync.Mutex
	rng := newRand()
	rngMu.Lock()
	l, err := New(128 * 1024)
	if err != nil {
		b.Fatalf("err: %v", err)
	}

	type traceEntry struct {
		k string
		v int64
	}
	trace := make([]traceEntry, b.N*2)
	for i := 0; i < b.N*2; i++ {
		var n int64
		switch i % 4 {
		case 0, 1:
			n = 0
		case 2:
			n = 1
		default:
			n = rng.Int63() % (4 * 128 * 1024)
		}
		trace[i] = traceEntry{k: strconv.Itoa(int(n)), v: n}
	}
	rngMu.Unlock()

	b.ResetTimer()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		rngMu.Lock()
		seed := rng.Intn(len(trace))
		rngMu.Unlock()

		var hit, miss int
		i := seed
		for pb.Next() {
			// use a predictable if rather than % len(trace) to eek a little more perf out
			if i >= len(trace) {
				i = 0
			}

			t := trace[i]
			if i%2 == 0 {
				l.Add(t.k, t.v)
			} else {
				if _, ok := l.Get(t.k); ok {
					hit++
				} else {
					miss++
				}
			}

			i++
		}
		// b.Logf("hit: %d miss: %d ratio: %f", hit, miss, float64(hit)/float64(miss))
	})
}
