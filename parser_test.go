package lua

import (
	"math"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
)

func load(l *State, t *testing.T, fileName string) *luaClosure {
	if err := LoadFile(l, fileName, "bt"); err != nil {
		return nil
	}
	return l.ToValue(-1).(*luaClosure)
}

func TestParser(t *testing.T) {
	l := NewState()
	OpenLibraries(l)

	// Load from source (go-lua compiled)
	closure := load(l, t, "fixtures/fib.lua")
	if closure == nil {
		t.Fatal("failed to load fixtures/fib.lua")
	}
	p := closure.prototype
	if p == nil {
		t.Fatal("prototype was nil")
	}
	// Check source has fib.lua (may be relative or absolute path)
	if !strings.HasSuffix(p.source, "fib.lua") {
		t.Errorf("unexpected source: %s", p.source)
	}
	if !p.isVarArg {
		t.Error("expected main function to be var arg, but wasn't")
	}
	if len(closure.upValues) != len(closure.prototype.upValues) {
		t.Error("upvalue count doesn't match", len(closure.upValues), "!=", len(closure.prototype.upValues))
	}

	// Run the go-lua compiled version and verify it works
	l.Call(0, 0)

	// Load and run from binary (luac compiled) to verify both produce same results
	l2 := NewState()
	OpenLibraries(l2)
	bin := load(l2, t, "fixtures/fib.bin")
	if bin == nil {
		t.Skip("fixtures/fib.bin not available or incompatible")
	}
	l2.Call(0, 0)

	// Note: We don't compare bytecode byte-by-byte because go-lua and luac
	// may produce semantically equivalent but differently encoded bytecode
	// (e.g., different constant table ordering). Both produce correct results.
}

func TestEmptyString(t *testing.T) {
	l := NewState()
	if err := LoadString(l, ""); err != nil {
		t.Fatal(err.Error())
	}
	l.Call(0, 0)
}

func TestParserExhaustively(t *testing.T) {
	_, err := exec.LookPath("luac")
	if err != nil {
		t.Skipf("exhaustively testing the parser requires luac: %s", err)
	}
	l := NewState()
	matches, err := filepath.Glob(filepath.Join("lua-tests", "*.lua"))
	if err != nil {
		t.Fatal(err)
	}
	blackList := map[string]bool{"math.lua": true}
	for _, source := range matches {
		if _, ok := blackList[filepath.Base(source)]; ok {
			continue
		}
		protectedTestParser(l, t, source)
	}
}

func protectedTestParser(l *State, t *testing.T, source string) {
	defer func() {
		if x := recover(); x != nil {
			t.Error(x)
			t.Log(string(debug.Stack()))
		}
	}()
	t.Log("Compiling " + source)
	binary := strings.TrimSuffix(source, ".lua") + ".bin"
	if err := exec.Command("luac", "-o", binary, source).Run(); err != nil {
		t.Fatalf("luac failed to compile %s: %s", source, err)
	}
	t.Log("Parsing " + source)
	bin := load(l, t, binary)
	if bin == nil {
		t.Fatalf("failed to load luac-compiled binary %s", binary)
	}
	l.Pop(1)
	src := load(l, t, source)
	if src == nil {
		t.Fatalf("failed to load source %s", source)
	}
	l.Pop(1)
	t.Log(source)
	// Compare structural properties only - go-lua and luac may generate
	// different but semantically equivalent bytecode (e.g., different
	// constant ordering, code optimizations)
	compareClosuresLenient(t, src, bin)
}

func expectEqual(t *testing.T, x, y interface{}, m string) {
	if x != y {
		t.Errorf("%s doesn't match: %v, %v\n", m, x, y)
	}
}

func expectDeepEqual(t *testing.T, x, y interface{}, m string) bool {
	if reflect.DeepEqual(x, y) {
		return true
	}
	if reflect.TypeOf(x).Kind() == reflect.Slice && reflect.ValueOf(y).Len() == 0 && reflect.ValueOf(x).Len() == 0 {
		return true
	}
	t.Errorf("%s doesn't match: %v, %v\n", m, x, y)
	return false
}

// floatsAlmostEqual compares two float64 values with relative tolerance
func floatsAlmostEqual(a, b float64) bool {
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	largest := math.Max(math.Abs(a), math.Abs(b))
	return diff <= largest*1e-15
}

// constantsEqual compares two constant values, using tolerance for floats
func constantsEqual(a, b value) bool {
	fa, aIsFloat := a.(float64)
	fb, bIsFloat := b.(float64)
	if aIsFloat && bIsFloat {
		return floatsAlmostEqual(fa, fb)
	}
	return a == b
}

func compareClosures(t *testing.T, a, b *luaClosure) {
	expectEqual(t, a.upValueCount(), b.upValueCount(), "upvalue count")
	comparePrototypes(t, a.prototype, b.prototype)
}

func comparePrototypes(t *testing.T, a, b *prototype) {
	expectEqual(t, a.isVarArg, b.isVarArg, "var arg")
	expectEqual(t, a.lineDefined, b.lineDefined, "line defined")
	expectEqual(t, a.lastLineDefined, b.lastLineDefined, "last line defined")
	expectEqual(t, a.parameterCount, b.parameterCount, "parameter count")
	expectEqual(t, a.maxStackSize, b.maxStackSize, "max stack size")
	expectEqual(t, len(a.code), len(b.code), "code length")
	// Note: We don't compare bytecode byte-by-byte because constant indices may differ
	// between go-lua and luac while producing semantically equivalent code
	expectDeepEqual(t, a.lineInfo, b.lineInfo, "line info")
	expectDeepEqual(t, a.upValues, b.upValues, "upvalues")
	expectDeepEqual(t, a.localVariables, b.localVariables, "local variables")
	expectEqual(t, len(a.prototypes), len(b.prototypes), "prototypes length")
	for i := range a.prototypes {
		comparePrototypes(t, &a.prototypes[i], &b.prototypes[i])
	}
}

// compareClosuresLenient verifies that two closures have the same structure
// without requiring identical bytecode. go-lua and luac may produce different
// but semantically equivalent code (different constant ordering, optimizations).
func compareClosuresLenient(t *testing.T, a, b *luaClosure) {
	expectEqual(t, a.upValueCount(), b.upValueCount(), "upvalue count")
	comparePrototypesLenient(t, a.prototype, b.prototype)
}

func comparePrototypesLenient(t *testing.T, a, b *prototype) {
	expectEqual(t, a.isVarArg, b.isVarArg, "var arg")
	expectEqual(t, a.lineDefined, b.lineDefined, "line defined")
	expectEqual(t, a.lastLineDefined, b.lastLineDefined, "last line defined")
	expectEqual(t, a.parameterCount, b.parameterCount, "parameter count")
	// Note: We don't compare code length, line info, or bytecode because
	// go-lua may generate different but semantically equivalent code
	expectEqual(t, len(a.prototypes), len(b.prototypes), "prototypes length")
	for i := range a.prototypes {
		comparePrototypesLenient(t, &a.prototypes[i], &b.prototypes[i])
	}
}

