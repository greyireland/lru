package main

import (
	"fmt"
	"github.com/greyireland/lru"
	"strconv"
)

func main() {
	l := lru.New[string, int](128)
	for i := 0; i < 256; i++ {
		l.Add(strconv.Itoa(i), i)
	}
	if l.Len() != 128 {
		panic(fmt.Sprintf("bad len: %v", l.Len()))
	}
	if v, ok := l.Get("200"); ok {
		fmt.Println(v) //200
	}
}
