package lua

import (
	"fmt"
	"testing"
)

func TestTableConstruct(t *testing.T) {
	l := NewState()
	OpenLibraries(l)

	snippets := []struct {
		name string
		code string
	}{
		{"empty", "local t = {}; return #t"},
		{"one", "local t = {42}; return t[1]"},
		{"three", "local t = {10, 20, 30}; return t[1]"},
		{"hash", "local t = {x=1}; return t.x"},
		{"mixed", "local t = {10, x=1}; return t[1]"},
		{"len", "local t = {10, 20, 30}; return #t"},
	}
	for _, s := range snippets {
		t.Run(s.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()
			ll := NewState()
			OpenLibraries(ll)
			err := LoadString(ll, s.code)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			ll.Call(0, 1)
			val := ll.ToValue(-1)
			fmt.Printf("[%s] result: %v\n", s.name, val)
			ll.Pop(1)
		})
	}
}
