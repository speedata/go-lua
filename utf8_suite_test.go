package lua

import (
	"path/filepath"
	"testing"
)

func TestUtf8Suite(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	for _, s := range []string{"_port", "_no32", "_noformatA", "_noweakref", "_noGC", "_noBuffering", "_noStringDump", "_nocoroutine", "_soft"} {
		l.PushBoolean(true)
		l.SetGlobal(s)
	}
	l.Global("package")
	l.PushString("./?.lua;./lua-tests/?.lua")
	l.SetField(-2, "path")
	l.Pop(1)
	l.Global("debug")
	l.Field(-1, "traceback")
	traceback := l.Top()
	if err := LoadFile(l, filepath.Join("lua-tests", "utf8.lua"), "text"); err != nil {
		t.Fatalf("LoadFile failed: %s", err.Error())
	}
	if err := l.ProtectedCall(0, 0, traceback); err != nil {
		t.Fatalf("failed: %s", err.Error())
	}
}
