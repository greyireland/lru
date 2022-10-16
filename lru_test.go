package lru

import (
	"fmt"
	"strconv"
	"testing"
)

func TestNew(t *testing.T) {
	l := New[string, int](128)
	for i := 0; i < 256; i++ {
		l.Add(strconv.Itoa(i), i)
	}
	if l.Len() != 128 {
		panic(fmt.Sprintf("bad len: %v", l.Len()))
	}
	if v, ok := l.Get("200"); ok {
		_ = v // use v
	}
}
