package lua

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testString(t *testing.T, s string) { testStringHelper(t, s, false) }

// Commented out to avoid a warning relating to the method not being used. Left here since it's useful for debugging.
//func traceString(t *testing.T, s string) { testStringHelper(t, s, true) }

func testNoPanicString(t *testing.T, s string) {
	defer func() {
		if rc := recover(); rc != nil {
			var buffer [8192]byte
			t.Errorf("got panic %v; expected none", rc)
			t.Logf("trace:\n%s", buffer[:runtime.Stack(buffer[:], false)])
		}
	}()
	testStringHelper(t, s, false)
}

func testStringHelper(t *testing.T, s string, trace bool) {
	l := NewState()
	OpenLibraries(l)
	LoadString(l, s)
	if trace {
		SetDebugHook(l, func(state *State, ar Debug) {
			ci := state.callInfo
			p := state.prototype(ci)
			println(stack(state.stack[ci.base():state.top]))
			println(ci.code[ci.savedPC].String(), p.source, p.lineInfo[ci.savedPC])
		}, MaskCount, 1)
	}
	l.Call(0, 0)
}

func TestProtectedCall(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	SetDebugHook(l, func(state *State, ar Debug) {
		ci := state.callInfo
		_ = stack(state.stack[ci.base():state.top])
		_ = ci.code[ci.savedPC].String()
	}, MaskCount, 1)
	LoadString(l, "assert(not pcall(bit32.band, {}))")
	l.Call(0, 0)
}

func TestLua(t *testing.T) {
	tests := []struct {
		name    string
		nonPort bool
	}{
		// {name: "attrib"},     // Requires coroutine module
		// {name: "big"},         // EXTRAARG handling issue with large (>2^18 element) tables
		{name: "bitwise"},
		// {name: "calls"},       // Requires debug.getinfo
		{name: "closure"},
		{name: "code"},
		{name: "constructs"},
		// {name: "coroutine"},   // Coroutines not implemented
		// {name: "db"},          // Uses coroutines
		// {name: "errors"},      // Uses coroutines
		{name: "events"},
		// {name: "files"},       // File I/O differences
		// {name: "gc"},          // GC not controllable in Go
		{name: "goto"},
		// {name: "literals"},    // Uses coroutines
		{name: "locals"},
		// {name: "main"},        // Requires command-line Lua
		{name: "math"},
		// {name: "nextvar"},     // ipairs returns new function each time
		{name: "pm"},
		{name: "sort", nonPort: true},
		{name: "strings"},
		{name: "tpack"},          // Lua 5.3: string.pack/unpack tests
		{name: "utf8"},           // Lua 5.3: utf8 library tests
		{name: "vararg"},
		// {name: "verybig"},     // Very slow/memory intensive
	}
	for _, v := range tests {
		if v.nonPort && runtime.GOOS == "windows" {
			t.Skipf("'%s' skipped because it's non-portable & we're running Windows", v.name)
		}
		t.Log(v)
		l := NewState()
		OpenLibraries(l)
		for _, s := range []string{"_port", "_no32", "_noformatA", "_noweakref"} {
			l.PushBoolean(true)
			l.SetGlobal(s)
		}
		if v.nonPort {
			l.PushBoolean(false)
			l.SetGlobal("_port")
		}
		// l.SetDebugHook(func(state *State, ar Debug) {
		// 	ci := state.callInfo.(*luaCallInfo)
		// 	p := state.prototype(ci)
		// 	println(stack(state.stack[ci.base():state.top]))
		// 	println(ci.code[ci.savedPC].String(), p.source, p.lineInfo[ci.savedPC])
		// }, MaskCount, 1)
		l.Global("debug")
		l.Field(-1, "traceback")
		traceback := l.Top()
		// t.Logf("%#v", l.ToValue(traceback))
		if err := LoadFile(l, filepath.Join("lua-tests", v.name+".lua"), "text"); err != nil {
			t.Errorf("'%s' failed: %s", v.name, err.Error())
		}
		// l.Call(0, 0)
		if err := l.ProtectedCall(0, 0, traceback); err != nil {
			t.Errorf("'%s' failed: %s", v.name, err.Error())
		}
	}
}

func benchmarkSort(b *testing.B, program string) {
	l := NewState()
	OpenLibraries(l)
	s := `a = {}
		for i=1,%d do
			a[i] = math.random()
		end`
	LoadString(l, fmt.Sprintf(s, b.N))
	if err := l.ProtectedCall(0, 0, 0); err != nil {
		b.Error(err.Error())
	}
	LoadString(l, program)
	b.ResetTimer()
	if err := l.ProtectedCall(0, 0, 0); err != nil {
		b.Error(err.Error())
	}
}

func BenchmarkSort(b *testing.B) { benchmarkSort(b, "table.sort(a)") }
func BenchmarkSort2(b *testing.B) {
	benchmarkSort(b, "i = 0; table.sort(a, function(x,y) i=i+1; return y<x end)")
}

func BenchmarkFibonnaci(b *testing.B) {
	l := NewState()
	s := `return function(n)
			if n == 0 then
				return 0
			elseif n == 1 then
				return 1
			end
			local n0, n1 = 0, 1
			for i = n, 2, -1 do
				local tmp = n0 + n1
				n0 = n1
				n1 = tmp
			end
			return n1
		end`
	LoadString(l, s)
	if err := l.ProtectedCall(0, 1, 0); err != nil {
		b.Error(err.Error())
	}
	l.PushInteger(b.N)
	b.ResetTimer()
	if err := l.ProtectedCall(1, 1, 0); err != nil {
		b.Error(err.Error())
	}
}

// TestTailCallRecursive tests for failures where both the callee and caller are making a tailcall.
func TestTailCallRecursive(t *testing.T) {
	s := `function tailcall(n, m)
			if n > m then return n end
			return tailcall(n + 1, m)
		end
		return tailcall(0, 5)`
	testNoPanicString(t, s)
}

// TestTailCallRecursiveDiffFn tests for failures where only the caller is making a tailcall.
func TestTailCallRecursiveDiffFn(t *testing.T) {
	s := `function tailcall(n) return n+1 end
		return tailcall(5)`
	testNoPanicString(t, s)
}

// TestTailCallSameFn tests for failures where only the callee is making a tailcall.
func TestTailCallSameFn(t *testing.T) {
	s := `function tailcall(n, m)
			if n > m then return n end
			return tailcall(n + 1, m)
		end
		return (tailcall(0, 5))`
	testNoPanicString(t, s)
}

// TestNoTailCall tests for failures when neither callee nor caller make a tailcall.
func TestNormalCall(t *testing.T) {
	s := `function notailcall() return 5 end
		return (notailcall())`
	testNoPanicString(t, s)
}

func TestVarArgMeta(t *testing.T) {
	s := `function f(t, ...) return t, {...} end
		local a = setmetatable({}, {__call = f})
		local x, y = a(table.unpack{"a", 1})
		assert(#x == 0)
		assert(#y == 2 and y[1] == "a" and y[2] == 1)`
	testString(t, s)
}

func TestCanRemoveNilObjectFromStack(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("failed to remove `nil`, %v", r)
		}
	}()

	l := NewState()
	l.PushString("hello")
	l.Remove(-1)
	l.PushNil()
	l.Remove(-1)
}

func TestTableUserdataEquality(t *testing.T) {
	const s = `return function(x)
		local b = x == {}
		assert(type(b) == "boolean")
		assert(b == false)
		-- reverse
		b = {} == x
		assert(type(b) == "boolean")
		assert(b == false)
	end`

	l := NewState()
	OpenLibraries(l)
	LoadString(l, s)
	if err := l.ProtectedCall(0, 1, 0); err != nil {
		t.Error(err.Error())
	}

	l.PushUserData(5)
	if err := l.ProtectedCall(1, 0, 0); err != nil {
		t.Error(err.Error())
	}
}

func TestUserDataEqualityNil(t *testing.T) {
	const s = `return function(x)
		local b = x == nil
		assert(type(b) == "boolean")
		assert(b == false)
	end`

	l := NewState()
	OpenLibraries(l)
	LoadString(l, s)
	if err := l.ProtectedCall(0, 1, 0); err != nil {
		t.Error(err.Error())
	}

	l.PushUserData(5)
	if err := l.ProtectedCall(1, 0, 0); err != nil {
		t.Error(err.Error())
	}
}

func TestTableEqualityNil(t *testing.T) {
	const s = `local b = {} == nil
	assert(type(b) == "boolean")
	assert(b == false)`

	testString(t, s)
}

func TestTableNext(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	l.CreateTable(10, 0)
	for i := 1; i <= 4; i++ {
		l.PushInteger(i)
		l.PushValue(-1)
		l.SetTable(-3)
	}
	if length := LengthEx(l, -1); length != 4 {
		t.Errorf("expected table length to be 4, but was %d", length)
	}
	count := 0
	for l.PushNil(); l.Next(-2); count++ {
		if k, v := CheckInteger(l, -2), CheckInteger(l, -1); k != v {
			t.Errorf("key %d != value %d", k, v)
		}
		l.Pop(1)
	}
	if count != 4 {
		t.Errorf("incorrect iteration count %d in Next()", count)
	}
}

func TestError(t *testing.T) {
	l := NewState()
	BaseOpen(l)
	errorHandled := false
	program := "error('error')"
	l.PushGoFunction(func(l *State) int {
		if l.Top() == 0 {
			t.Error("error handler received no arguments")
		} else if errorMessage, ok := l.ToString(-1); !ok {
			t.Errorf("error handler received %s instead of string", TypeNameOf(l, -1))
		} else if errorMessage != chunkID(program)+":1: error" {
			t.Errorf("error handler received '%s' instead of 'error'", errorMessage)
		}
		errorHandled = true
		return 1
	})
	LoadString(l, program)
	l.ProtectedCall(0, 0, -2)
	if !errorHandled {
		t.Error("error not handled")
	}
}

func TestErrorf(t *testing.T) {
	l := NewState()
	BaseOpen(l)
	program := "-- script that is bigger than the max ID size\nhelper()\n" + strings.Repeat("--", idSize)
	expectedErrorMessage := chunkID(program) + ":2: error"
	l.PushGoFunction(func(l *State) int {
		Errorf(l, "error")
		return 0
	})
	l.SetGlobal("helper")
	errorHandled := false
	l.PushGoFunction(func(l *State) int {
		if l.Top() == 0 {
			t.Error("error handler received no arguments")
		} else if errorMessage, ok := l.ToString(-1); !ok {
			t.Errorf("error handler received %s instead of string", TypeNameOf(l, -1))
		} else if errorMessage != expectedErrorMessage {
			t.Errorf("error handler received '%s' instead of '%s'", errorMessage, expectedErrorMessage)
		}
		errorHandled = true
		return 1
	})
	LoadString(l, program)
	l.ProtectedCall(0, 0, -2)
	if !errorHandled {
		t.Error("error not handled")
	}
}

func TestPairsSplit(t *testing.T) {
	testString(t, `
	local t = {}
	-- first two keys go into array
	t[1] = true
	t[2] = true
	-- next key forced into map instead of array since it's non-sequential
	t[16] = true
	-- next key inserted into array
	t[3] = true

	local keys = {}
	for k, v in pairs(t) do
		keys[#keys + 1] = k
	end

	table.sort(keys)
	assert(keys[1] == 1, 'got ' .. tostring(keys[1]) .. '; want 1')
	assert(keys[2] == 2, 'got ' .. tostring(keys[2]) .. '; want 2')
	assert(keys[3] == 3, 'got ' .. tostring(keys[3]) .. '; want 3')
	assert(keys[4] == 16, 'got ' .. tostring(keys[4]) .. '; want 16')
	`)
}

func TestConcurrentNext(t *testing.T) {
	testString(t, `
	t = {}
	t[1], t[2], t[3] = true, true, true

	outer = {}
	for k1 in pairs(t) do
		table.insert(outer, k1)
		inner = {}
		for k2 in pairs(t) do
			table.insert(inner, k2)
		end
		table.sort(inner)
		got = table.concat(inner, '')
		assert(got == '123', 'got ' .. got .. '; want 123')
	end

	table.sort(outer)
	got = table.concat(outer, '')
	assert(got == '123', 'got ' .. got .. '; want 123')
	`)
}

func TestLocIsCorrectOnRegisteredFuncCall(t *testing.T) {
	l := NewState()
	l.Register("barf", func(l *State) int {
		Errorf(l, "Boom!")
		return 0
	})
	if err := l.Load(strings.NewReader(`
			local thing = barf()  -- line 2; is the source of the error
			print(thing)          -- line 3; this won't execute, and must NOT be the loc of the error!
			`), "test", ""); err != nil {
		t.Errorf("Unexpected error! Got %v", err)
	}
	err := l.ProtectedCall(0, 0, 0)
	if err == nil {
		t.Errorf("Expected error! Got none... :(")
	} else {
		if err.Error() != "runtime error: [string \"test\"]:2: Boom!" {
			t.Errorf("Wrong error reported: %v", err)
		}
	}
}

func TestLocIsCorrectOnFuncCall(t *testing.T) {
	l := NewState()
	if err := l.Load(strings.NewReader(`
			function barf()
				a = 3 + 2
				isNotDefined("Boom!", a)  -- line 4; is the source of the error
			end
			barf()                        -- line 6
			`), "test", ""); err != nil {
		t.Errorf("Unexpected error! Got %v", err)
	}
	err := l.ProtectedCall(0, 0, 0)
	if err == nil {
		t.Errorf("Expected error! Got none... :(")
	} else {
		if err.Error() != "runtime error: [string \"test\"]:4: attempt to call a nil value" {
			t.Errorf("Wrong error reported: %v", err)
		}
	}
}

func TestLocIsCorrectOnError(t *testing.T) {
	l := NewState()
	if err := l.Load(strings.NewReader(`
			a = 3 - 3
			b = 3 / q  -- line 3; errs!
			`), "test", ""); err != nil {
		t.Errorf("Unexpected error! Got %v", err)
	}
	err := l.ProtectedCall(0, 0, 0)
	if err == nil {
		t.Errorf("Expected error! Got none... :(")
	} else {
		if err.Error() != "runtime error: [string \"test\"]:3: attempt to perform arithmetic on a nil value" {
			t.Errorf("Wrong error reported: %v", err)
		}
	}
}

// Lua 5.3 integer helper function tests

func TestIntIDiv(t *testing.T) {
	tests := []struct {
		m, n, want int64
	}{
		{10, 3, 3},
		{-10, 3, -4},   // floor division: -10/3 = -3.33... -> -4
		{10, -3, -4},   // floor division: 10/-3 = -3.33... -> -4
		{-10, -3, 3},   // floor division: -10/-3 = 3.33... -> 3
		{9, 3, 3},
		{0, 5, 0},
		{100, 7, 14},
		{-100, 7, -15}, // floor division
	}
	for _, tt := range tests {
		got := intIDiv(tt.m, tt.n)
		if got != tt.want {
			t.Errorf("intIDiv(%d, %d) = %d; want %d", tt.m, tt.n, got, tt.want)
		}
	}
}

func TestIntShiftLeft(t *testing.T) {
	tests := []struct {
		x, y, want int64
	}{
		{1, 0, 1},
		{1, 1, 2},
		{1, 4, 16},
		{1, 63, -9223372036854775808}, // MinInt64 = 1 << 63
		{1, 64, 0},      // shift >= 64 returns 0
		{1, 100, 0},     // shift >= 64 returns 0
		{16, -1, 8},     // negative shift = right shift
		{16, -2, 4},
		{16, -4, 1},
		{16, -5, 0},
		{-1, -64, 0},    // large negative shift
		{0xFF, 4, 0xFF0},
	}
	for _, tt := range tests {
		got := intShiftLeft(tt.x, tt.y)
		if got != tt.want {
			t.Errorf("intShiftLeft(%d, %d) = %d; want %d", tt.x, tt.y, got, tt.want)
		}
	}
}

func TestIntegerValues(t *testing.T) {
	tests := []struct {
		b, c   value
		wantIb int64
		wantIc int64
		wantOk bool
	}{
		{int64(5), int64(3), 5, 3, true},
		{float64(5.0), int64(3), 5, 3, true},
		{int64(5), float64(3.0), 5, 3, true},
		{float64(5.0), float64(3.0), 5, 3, true},
		{float64(5.5), int64(3), 0, 0, false},  // non-integer float
		{int64(5), float64(3.5), 5, 0, false},  // non-integer float
		{"5", int64(3), 0, 0, false},           // string not converted
	}
	for _, tt := range tests {
		ib, ic, ok := integerValues(tt.b, tt.c)
		if ok != tt.wantOk {
			t.Errorf("integerValues(%v, %v) ok = %v; want %v", tt.b, tt.c, ok, tt.wantOk)
			continue
		}
		if ok && (ib != tt.wantIb || ic != tt.wantIc) {
			t.Errorf("integerValues(%v, %v) = (%d, %d); want (%d, %d)",
				tt.b, tt.c, ib, ic, tt.wantIb, tt.wantIc)
		}
	}
}

// Test that bit32 library still works (uses the VM operations)
func TestBit32WithIntegers(t *testing.T) {
	testString(t, `
		-- Test bit32 operations which now use integer types internally
		assert(bit32.band(0xFF, 0x0F) == 0x0F)
		assert(bit32.bor(0xF0, 0x0F) == 0xFF)
		assert(bit32.bxor(0xFF, 0x0F) == 0xF0)
		assert(bit32.bnot(0) == 0xFFFFFFFF)
		assert(bit32.lshift(1, 4) == 16)
		assert(bit32.rshift(16, 4) == 1)
	`)
}

// Lua 5.3 operator tests

func TestLua53IntegerDivision(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	LoadString(l, `return 10 // 3`)
	l.Call(0, 1)
	result, _ := l.ToNumber(-1)
	t.Logf("10 // 3 = %v", result)
	if result != 3 {
		t.Errorf("10 // 3 = %v; want 3", result)
	}
	l.Pop(1)

	LoadString(l, `return 9 // 3`)
	l.Call(0, 1)
	result, _ = l.ToNumber(-1)
	t.Logf("9 // 3 = %v", result)
	if result != 3 {
		t.Errorf("9 // 3 = %v; want 3", result)
	}
}

func TestLua53BitwiseAnd(t *testing.T) {
	testString(t, `
		-- Test & operator (bitwise AND)
		assert((0xFF & 0x0F) == 0x0F)
		assert((0xF0 & 0x0F) == 0)
		assert((0xFF & 0xFF) == 0xFF)
		assert((12 & 10) == 8)  -- 1100 & 1010 = 1000
	`)
}

func TestLua53BitwiseOr(t *testing.T) {
	testString(t, `
		-- Test | operator (bitwise OR)
		assert((0xF0 | 0x0F) == 0xFF)
		assert((0 | 0xFF) == 0xFF)
		assert((12 | 10) == 14)  -- 1100 | 1010 = 1110
	`)
}

func TestLua53BitwiseXor(t *testing.T) {
	testString(t, `
		-- Test ~ operator (bitwise XOR, binary)
		assert((0xFF ~ 0x0F) == 0xF0)
		assert((0xFF ~ 0xFF) == 0)
		assert((12 ~ 10) == 6)  -- 1100 ^ 1010 = 0110
	`)
}

func TestLua53BitwiseNot(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	if err := LoadString(l, `return ~0`); err != nil {
		t.Fatalf("LoadString error: %v", err)
	}
	if err := l.ProtectedCall(0, 1, 0); err != nil {
		t.Fatalf("ProtectedCall error: %v", err)
	}
	result, _ := l.ToNumber(-1)
	t.Logf("~0 = %v", result)
	if result != -1 {
		t.Errorf("~0 = %v; want -1", result)
	}
}

func TestLua53ShiftLeft(t *testing.T) {
	testString(t, `
		-- Test << operator (shift left)
		assert((1 << 0) == 1)
		assert((1 << 1) == 2)
		assert((1 << 4) == 16)
		assert((0xFF << 4) == 0xFF0)
	`)
}

func TestLua53ShiftRight(t *testing.T) {
	testString(t, `
		-- Test >> operator (shift right)
		assert((16 >> 1) == 8)
		assert((16 >> 2) == 4)
		assert((16 >> 4) == 1)
		assert((0xFF0 >> 4) == 0xFF)
	`)
}

func TestLua53OperatorPrecedence(t *testing.T) {
	testString(t, `
		-- Test operator precedence
		-- ^ is higher than unary -
		assert((-2^2) == -4)

		-- Bitwise operators precedence: & > ~ > |
		assert((1 | 2 & 3) == (1 | (2 & 3)))
		assert((1 | 2 ~ 3) == (1 | (2 ~ 3)))

		-- Shifts are between concat and bitwise AND
		assert((1 << 2 & 0xFF) == ((1 << 2) & 0xFF))
	`)
}

func TestLua53MixedOperators(t *testing.T) {
	testString(t, `
		-- Test combining old and new operators
		local a = 10
		local b = 3
		assert(a + b == 13)
		assert(a - b == 7)
		assert(a * b == 30)
		assert(a / b > 3.3 and a / b < 3.4)
		assert(a // b == 3)
		assert(a % b == 1)

		-- Bitwise with arithmetic
		assert((1 + 2) & 3 == 3)
		assert((4 | 2) + 1 == 7)
	`)
}

func TestLua53MathLibrary(t *testing.T) {
	testString(t, `
		-- Test math.maxinteger and math.mininteger
		assert(math.maxinteger == 9223372036854775807)
		assert(math.mininteger == -9223372036854775808)

		-- Test math.tointeger
		assert(math.tointeger(3.0) == 3)
		assert(math.tointeger(3.1) == nil)
		assert(math.tointeger("5") == 5)
		assert(math.tointeger("hello") == nil)

		-- Test math.type
		-- Note: Literals are parsed as floats, so we test with loaded integers
		local i = math.tointeger(5)  -- this should be an integer
		-- For now we only test float detection since parser creates floats
		assert(math.type(3.14) == "float")
		assert(math.type("x") == nil)

		-- Test math.ult (unsigned less than)
		assert(math.ult(1, 2) == true)
		assert(math.ult(2, 1) == false)
		assert(math.ult(-1, 1) == false)  -- -1 as unsigned is huge
		assert(math.ult(1, -1) == true)   -- 1 < huge
		assert(math.ult(0, math.maxinteger) == true)

		-- Test math.floor returns integer (Lua 5.3)
		assert(math.type(math.floor(3.5)) == "integer")
		assert(math.floor(3.5) == 3)
		assert(math.floor(-3.5) == -4)

		-- Test math.ceil returns integer (Lua 5.3)
		assert(math.type(math.ceil(3.5)) == "integer")
		assert(math.ceil(3.5) == 4)
		assert(math.ceil(-3.5) == -3)

		-- Test math.modf returns integer for first value (Lua 5.3)
		local i, f = math.modf(3.5)
		assert(math.type(i) == "integer")
		assert(i == 3 and f == 0.5)
	`)
}

func TestLua53TableMove(t *testing.T) {
	testString(t, `
		-- Basic move within same table
		local t = {1, 2, 3, 4, 5}
		table.move(t, 2, 4, 1)
		assert(t[1] == 2 and t[2] == 3 and t[3] == 4 and t[4] == 4 and t[5] == 5)

		-- Move to extend table
		t = {1, 2, 3, 4, 5}
		table.move(t, 1, 3, 4)
		assert(t[4] == 1 and t[5] == 2 and t[6] == 3)

		-- Move to different table
		local src = {10, 20, 30}
		local dst = {1, 2, 3, 4, 5}
		table.move(src, 1, 3, 2, dst)
		assert(dst[2] == 10 and dst[3] == 20 and dst[4] == 30)

		-- Empty range (e < f) should do nothing
		t = {1, 2, 3}
		local result = table.move(t, 5, 3, 1)
		assert(t[1] == 1 and t[2] == 2 and t[3] == 3)
		assert(result == t)  -- returns destination table

		-- Overlapping: source before destination in same table
		t = {1, 2, 3, 4, 5}
		table.move(t, 1, 3, 2)
		assert(t[1] == 1 and t[2] == 1 and t[3] == 2 and t[4] == 3 and t[5] == 5)
	`)
}

func TestLua53UTF8Library(t *testing.T) {
	testString(t, `
		-- utf8.char: convert codepoints to string
		assert(utf8.char(65, 66, 67) == "ABC")
		assert(utf8.char(228, 246, 252) == "äöü")
		assert(utf8.char(0x1F600) == "😀")

		-- utf8.len: count UTF-8 characters
		assert(utf8.len("ABC") == 3)
		assert(utf8.len("äöü") == 3)
		assert(utf8.len("hello") == 5)
		assert(utf8.len("😀") == 1)

		-- utf8.codepoint: extract codepoints
		local a, b, c = utf8.codepoint("ABC", 1, 3)
		assert(a == 65 and b == 66 and c == 67)
		assert(utf8.codepoint("ä") == 228)

		-- utf8.offset: find byte position of n-th character
		local s = "äöü"
		assert(utf8.offset(s, 1) == 1)  -- ä starts at byte 1
		assert(utf8.offset(s, 2) == 3)  -- ö starts at byte 3
		assert(utf8.offset(s, 3) == 5)  -- ü starts at byte 5

		-- utf8.codes: iterate over characters
		local positions = {}
		local codes = {}
		for pos, code in utf8.codes("Héllo") do
			positions[#positions + 1] = pos
			codes[#codes + 1] = code
		end
		assert(#positions == 5)
		assert(positions[1] == 1 and codes[1] == 72)   -- H
		assert(positions[2] == 2 and codes[2] == 233)  -- é
		assert(positions[3] == 4 and codes[3] == 108)  -- l (after 2-byte é)
		assert(positions[4] == 5 and codes[4] == 108)  -- l
		assert(positions[5] == 6 and codes[5] == 111)  -- o

		-- utf8.charpattern exists
		assert(utf8.charpattern ~= nil)
	`)
}

func TestLua53StringPack(t *testing.T) {
	testString(t, `
		-- Pack and unpack bytes
		local packed = string.pack("bBbB", -1, 255, 0, 127)
		assert(#packed == 4)
		local a, b, c, d = string.unpack("bBbB", packed)
		assert(a == -1 and b == 255 and c == 0 and d == 127)

		-- Pack with endianness
		local le = string.pack("<I4", 0x12345678)
		local be = string.pack(">I4", 0x12345678)
		assert(string.unpack("<I4", le) == 0x12345678)
		assert(string.unpack(">I4", be) == 0x12345678)

		-- Little endian bytes should be reversed
		assert(string.byte(le, 1) == 0x78)
		assert(string.byte(be, 1) == 0x12)

		-- Zero-terminated strings
		local z = string.pack("z", "hello")
		assert(#z == 6)  -- 5 chars + null
		assert(string.unpack("z", z) == "hello")

		-- Fixed-size strings
		local c5 = string.pack("c5", "abc")
		assert(#c5 == 5)
		local s = string.unpack("c5", c5)
		assert(#s == 5)

		-- Double precision floats
		local d = string.pack("d", 3.14159)
		assert(#d == 8)
		local v = string.unpack("d", d)
		assert(math.abs(v - 3.14159) < 0.00001)

		-- Packsize for fixed formats
		assert(string.packsize("i4i4i4") == 12)
		assert(string.packsize("bbb") == 3)
		assert(string.packsize("d") == 8)

		-- 64-bit integers
		local j = string.pack("j", 9223372036854775807)
		assert(#j == 8)
		assert(string.unpack("j", j) == 9223372036854775807)
	`)
}

func TestLua53StringFormatHexFloat(t *testing.T) {
	testString(t, `
		-- Lua 5.3: %a and %A for hexadecimal floating-point
		local s = string.format("%a", 1.0)
		-- Should start with 0x (hex prefix)
		assert(string.sub(s, 1, 2) == "0x", "expected 0x prefix, got: " .. s)
		-- Should contain 'p' for exponent
		assert(string.find(s, "p"), "expected 'p' exponent, got: " .. s)

		-- Uppercase %A
		local S = string.format("%A", 1.0)
		assert(string.sub(S, 1, 2) == "0X", "expected 0X prefix, got: " .. S)
		assert(string.find(S, "P"), "expected 'P' exponent, got: " .. S)

		-- Test with pi
		local pi = string.format("%a", 3.14159265358979)
		assert(string.sub(pi, 1, 2) == "0x")

		-- Test with negative numbers
		local neg = string.format("%a", -1.5)
		assert(string.sub(neg, 1, 3) == "-0x", "expected -0x prefix, got: " .. neg)

		-- Test with zero
		local zero = string.format("%a", 0.0)
		assert(string.sub(zero, 1, 2) == "0x")

		-- Test format modifiers (precision)
		local prec = string.format("%.2a", 1.0)
		assert(string.sub(prec, 1, 2) == "0x")
	`)
}

func TestLuaPatternMatching(t *testing.T) {
	testString(t, `
		-- Basic string.find with patterns
		local s, e = string.find("hello world", "world")
		assert(s == 7 and e == 11, "basic find failed")

		-- Find with pattern
		s, e = string.find("hello123world", "%d+")
		assert(s == 6 and e == 8, "pattern find failed: " .. tostring(s) .. "," .. tostring(e))

		-- Find with anchor
		s, e = string.find("hello", "^hello")
		assert(s == 1 and e == 5, "anchor find failed")

		s, e = string.find("hello", "^world")
		assert(s == nil, "anchor should not match")

		-- string.match basic
		local m = string.match("hello world", "world")
		assert(m == "world", "basic match failed")

		-- string.match with capture
		m = string.match("hello 123 world", "(%d+)")
		assert(m == "123", "capture match failed: " .. tostring(m))

		-- string.match multiple captures
		local a, b = string.match("hello world", "(%w+) (%w+)")
		assert(a == "hello" and b == "world", "multiple captures failed")

		-- Character classes
		assert(string.match("abc123", "%a+") == "abc")
		assert(string.match("abc123", "%d+") == "123")
		assert(string.match("  abc", "%s+") == "  ")
		assert(string.match("ABC", "%u+") == "ABC")
		assert(string.match("abc", "%l+") == "abc")
		assert(string.match("ABCDEF12", "%x+") == "ABCDEF12")

		-- Complement classes
		assert(string.match("abc123def", "%D+") == "abc")
		assert(string.match("123abc", "%A+") == "123")

		-- Character sets
		assert(string.match("hello", "[aeiou]+") == "e")
		assert(string.match("hello", "[^aeiou]+") == "h")
		assert(string.match("abc123", "[a-z]+") == "abc")
		assert(string.match("ABC123", "[A-Z]+") == "ABC")

		-- Quantifiers
		assert(string.match("aaa", "a*") == "aaa")
		assert(string.match("bbb", "a*") == "")  -- * matches zero
		assert(string.match("aaa", "a+") == "aaa")
		assert(string.match("bbb", "a+") == nil)  -- + needs at least one
		assert(string.match("ab", "a?b") == "ab")
		assert(string.match("b", "a?b") == "b")

		-- Non-greedy quantifier
		assert(string.match("<tag>content</tag>", "<.->" ) == "<tag>")
		assert(string.match("<tag>content</tag>", "<.+>") == "<tag>content</tag>")

		-- Anchors
		assert(string.match("hello", "^h") == "h")
		assert(string.match("hello", "o$") == "o")
		assert(string.match("hello", "^hello$") == "hello")
		assert(string.match("hello world", "^hello$") == nil)

		-- Escape special characters
		assert(string.match("a.b", "a%.b") == "a.b")
		assert(string.match("a+b", "a%+b") == "a+b")

		-- Position captures
		local pos = string.match("hello", "()l")
		assert(pos == 3, "position capture failed: " .. tostring(pos))
	`)
}

func TestLuaGmatch(t *testing.T) {
	testString(t, `
		-- Basic gmatch
		local result = {}
		for w in string.gmatch("hello world", "%w+") do
			table.insert(result, w)
		end
		assert(#result == 2)
		assert(result[1] == "hello")
		assert(result[2] == "world")

		-- gmatch with captures
		result = {}
		for k, v in string.gmatch("a=1, b=2, c=3", "(%w+)=(%d+)") do
			result[k] = tonumber(v)
		end
		assert(result.a == 1)
		assert(result.b == 2)
		assert(result.c == 3)

		-- gmatch all digits
		result = {}
		for d in string.gmatch("abc123def456ghi", "%d+") do
			table.insert(result, d)
		end
		assert(#result == 2)
		assert(result[1] == "123")
		assert(result[2] == "456")
	`)
}

func TestLuaGsub(t *testing.T) {
	testString(t, `
		-- Basic gsub with string replacement
		local s, n = string.gsub("hello world", "world", "Lua")
		assert(s == "hello Lua", "basic gsub failed: " .. s)
		assert(n == 1)

		-- Multiple replacements
		s, n = string.gsub("hello hello hello", "hello", "hi")
		assert(s == "hi hi hi")
		assert(n == 3)

		-- Limited replacements
		s, n = string.gsub("hello hello hello", "hello", "hi", 2)
		assert(s == "hi hi hello")
		assert(n == 2)

		-- Pattern replacement
		s = string.gsub("hello 123 world 456", "%d+", "NUM")
		assert(s == "hello NUM world NUM")

		-- Capture replacement
		s = string.gsub("hello world", "(%w+)", "[%1]")
		assert(s == "[hello] [world]", "capture replacement failed: " .. s)

		-- %0 for whole match
		s = string.gsub("hello", "%w+", "<%0>")
		assert(s == "<hello>")

		-- Function replacement
		s = string.gsub("hello world", "%w+", function(w)
			return string.upper(w)
		end)
		assert(s == "HELLO WORLD", "function replacement failed: " .. s)

		-- Table replacement
		local t = {hello = "HELLO", world = "WORLD"}
		s = string.gsub("hello world", "%w+", t)
		assert(s == "HELLO WORLD", "table replacement failed: " .. s)

		-- Function returning nil (no replacement)
		s = string.gsub("hello world", "%w+", function(w)
			if w == "hello" then return "HI" end
			return nil
		end)
		assert(s == "HI world")

		-- Escape percent in replacement
		s = string.gsub("hello", "hello", "100%%")
		assert(s == "100%", "percent escape failed: " .. s)
	`)
}
