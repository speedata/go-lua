package lua

import (
	"slices"
	"testing"
)

// TestMathRandomseedReproducibleWithinState guards against the
// math.randomseed regression where Go 1.20+ turned the top-level
// rand.Seed into a no-op against the autoseeded global generator.
// Two sequences seeded with the same value in the same lua.State
// must be identical.
func TestMathRandomseedReproducibleWithinState(t *testing.T) {
	const code = `
		local function sequence()
			math.randomseed(20260516)
			local out = {}
			for i = 1, 10 do out[i] = math.random(-1000, 1000) end
			return out
		end
		local a = sequence()
		local b = sequence()
		for i = 1, #a do
			assert(a[i] == b[i], string.format(
				"randomseed(20260516) not reproducible at i=%d: a=%d b=%d",
				i, a[i], b[i]))
		end
	`
	l := NewState()
	OpenLibraries(l)
	if err := LoadString(l, code); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	if err := l.ProtectedCall(0, 0, 0); err != nil {
		t.Fatalf("randomseed reproducibility check failed: %v", err)
	}
}

// TestMathRandomseedReproducibleAcrossStates checks that two
// independent States seeded with the same value produce the same
// math.random sequence. This is the property that embedders such
// as glu rely on: separate processes each constructing a fresh
// State and calling randomseed(N) must produce byte-identical
// output.
func TestMathRandomseedReproducibleAcrossStates(t *testing.T) {
	runSeq := func() []int64 {
		l := NewState()
		OpenLibraries(l)
		const code = `
			math.randomseed(20260516)
			r1 = math.random(-1000, 1000)
			r2 = math.random(-1000, 1000)
			r3 = math.random(-1000, 1000)
			r4 = math.random(-1000, 1000)
			r5 = math.random(-1000, 1000)
		`
		if err := LoadString(l, code); err != nil {
			t.Fatalf("LoadString: %v", err)
		}
		if err := l.ProtectedCall(0, 0, 0); err != nil {
			t.Fatalf("ProtectedCall: %v", err)
		}
		out := make([]int64, 5)
		for i, name := range []string{"r1", "r2", "r3", "r4", "r5"} {
			l.Global(name)
			v, ok := l.ToInteger64(-1)
			if !ok {
				t.Fatalf("global %s is not an integer", name)
			}
			out[i] = v
			l.Pop(1)
		}
		return out
	}
	a := runSeq()
	b := runSeq()
	if !slices.Equal(a, b) {
		t.Fatalf("randomseed(20260516) not reproducible across States: a=%v b=%v", a, b)
	}
}
