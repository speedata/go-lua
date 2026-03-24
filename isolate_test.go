package lua

import (
	"fmt"
	"testing"
)

func TestIsolatePMBigStrings(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"big_find1", "local a = string.rep('a', 300000); assert(string.find(a, '^a*.?$'))"},
		{"big_find2", "local a = string.rep('a', 300000); assert(not string.find(a, '^a*.?b$'))"},
		{"big_find3", "local a = string.rep('a', 300000); assert(string.find(a, '^a-.?$'))"},
		{"big_gsub_no_repl", "local a = string.rep('a', 10000) .. string.rep('b', 10000); assert(not pcall(string.gsub, a, 'b'))"},
		{"rev", "local function rev(s) return string.gsub(s, '(.)(.+)', function(c,s1) return rev(s1)..c end) end; assert(rev(rev('abcdef')) == 'abcdef')"},
		{"gsub_table_empty", "assert(string.gsub('alo alo', '.', {}) == 'alo alo')"},
		{"gsub_table_match", "assert(string.gsub('alo alo', '(.)', {a='AA', l=''}) == 'AAo AAo')"},
		{"gsub_pos_table", "assert(string.gsub('alo alo', '().', {'x','yy','zzz'}) == 'xyyzzz alo')"},
		{"format_p_reuse", "local s = string.rep('a', 100); local r = string.gsub(s, 'b', 'c'); assert(string.format('%p', s) == string.format('%p', r))"},
		{"format_p_table_norepl", "local s = string.rep('a',100); local r = string.gsub(s, '.', {x='y'}); assert(string.format('%p',s) == string.format('%p',r))"},
		{"format_p_func_nil", "local s = string.rep('a',100); local c=0; local r = string.gsub(s, '.', function(x) c=c+1; return nil end); assert(string.format('%p',s) == string.format('%p',r))"},
		{"format_p_func_same", "local s = string.rep('a',100); local c=0; local r = string.gsub(s, '.', function(x) c=c+1; return x end); assert(r==s and string.format('%p',s) ~= string.format('%p',r))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewState()
			OpenLibraries(l)
			if err := LoadString(l, tt.code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			if err := l.ProtectedCall(0, 0, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
		})
	}
}

func TestIsolatePMGsubTable(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"empty_table", "assert(string.gsub('alo alo', '.', {}) == 'alo alo')"},
		{"table_match", "assert(string.gsub('alo alo', '(.)', {a='AA', l=''}) == 'AAo AAo')"},
		{"table_pair", "assert(string.gsub('alo alo', '(.)', {a='AA', l='K'}) == 'AAKo AAKo')"},
		{"table_pos", "assert(string.gsub('alo alo', '().', {'x','yy','zzz'}) == 'xyyzzz alo')"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewState()
			OpenLibraries(l)
			if err := LoadString(l, tt.code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			if err := l.ProtectedCall(0, 0, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
		})
	}
}

func TestIsolatePM(t *testing.T) {
	l := NewState()
	OpenLibraries(l)

	tests := []struct {
		name string
		code string
	}{
		{"empty_match", "assert(string.gsub('a b cd', ' *', '-') == '-a-b-c-d-')"},
		{"gmatch_init", "local s=0; for k in string.gmatch('10 20 30', '%d+', 3) do s=s+tonumber(k) end; assert(s==50, 'got '..s)"},
		{"format_p", "local s='abc'; assert(string.format('%p', s))"},
		{"gsub_norepl", "local s = string.rep('a', 10); local r = string.gsub(s, 'b', 'c'); assert(s == r)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ll := NewState()
			OpenLibraries(ll)
			if err := LoadString(ll, tt.code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			if err := ll.ProtectedCall(0, 0, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
		})
	}
}

func TestIsolateErrorsBasic(t *testing.T) {
	doit := "local function doit(s)\n" +
		"  local f, msg = load(s)\n" +
		"  if not f then return msg end\n" +
		"  local cond, msg = pcall(f)\n" +
		"  return (not cond) and msg\n" +
		"end\n"
	check := "local m = doit(prog)\n" +
		"if not m then error('no error for: ' .. prog) end\n" +
		"if not string.find(m, msg, 1, true) then\n" +
		"  error('expected [' .. msg .. '] in: ' .. tostring(m))\n" +
		"end\n"

	tests := []struct {
		name string
		prog string
		msg  string
	}{
		{"arithmetic", "a = {} + 1", "arithmetic"},
		{"bitwise", "a = {} | 1", "bitwise operation"},
		{"compare_lt", "a = {} < 1", "attempt to compare"},
		{"compare_le", "a = {} <= 1", "attempt to compare"},
		{"length_func", "aaa = #print", "length of a function value"},
		{"length_num", "aaa = #3", "length of a number value"},
		{"concat_table", "aaa=(1)..{}", "a table value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewState()
			OpenLibraries(l)
			code := doit + "local prog = [=[" + tt.prog + "]=]\n" +
				"local msg = '" + tt.msg + "'\n" + check
			if err := LoadString(l, code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			if err := l.ProtectedCall(0, 0, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
		})
	}
}

func TestIsolateCalls(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	// Use actual test parameters: n=10000, depth=100
	code := "local n = 10000\n" +
		"local function foo()\n" +
		"  if n == 0 then return 1023\n" +
		"  else n = n - 1; return foo()\n" +
		"  end\n" +
		"end\n" +
		"for i = 1, 100 do\n" +
		"  foo = setmetatable({}, {__call = foo})\n" +
		"end\n" +
		"return coroutine.wrap(function() return foo() end)()"
	if err := LoadString(l, code); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	if err := l.ProtectedCall(0, 1, 0); err != nil {
		t.Fatalf("Error: %v", err)
	}
	v, _ := l.ToInteger(-1)
	t.Logf("Result: %d", v)
	if v != 1023 {
		t.Errorf("expected 1023, got %d", v)
	}
}

func TestIsolateCallsStackOverflow(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	// Just the C-stack overflow test - does it work at all?
	code := "local function loop()\n" +
		"  assert(pcall(loop))\n" +
		"end\n" +
		"local err, msg = xpcall(loop, loop)\n" +
		"return err, msg"
	if err := LoadString(l, code); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	if err := l.ProtectedCall(0, 2, 0); err != nil {
		t.Fatalf("Error: %v", err)
	}
	t.Logf("err=%v msg=%v", l.ToValue(-2), l.ToValue(-1))
}

func TestIsolateCallsAfterOverflow(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	// C-stack overflow followed by simple function call
	code := "do\n" +
		"  local function loop()\n" +
		"    assert(pcall(loop))\n" +
		"  end\n" +
		"  local err, msg = xpcall(loop, loop)\n" +
		"end\n" +
		"return 42"
	if err := LoadString(l, code); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	if err := l.ProtectedCall(0, 1, 0); err != nil {
		t.Fatalf("Error: %v", err)
	}
	v, _ := l.ToInteger(-1)
	t.Logf("Result: %d", v)
	if v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
}

func TestIsolatePMGsubFalse(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"empty_table", `assert(string.gsub("alo alo", ".", {}) == "alo alo")`},
		{"table_match", `assert(string.gsub("alo alo", "(.)", {a="AA", l=""}) == "AAo AAo")`},
		{"table_pair", `assert(string.gsub("alo alo", "(.).", {a="AA", l="K"}) == "AAo AAo")`},
		{"table_false", `assert(string.gsub("alo alo", "((.)(.?))", {al="AA", o=false}) == "AAo AAo")`},
		{"func_nil_maxn", `t = {n=0}; assert(string.gsub("first second word", "%w+", function(w) t.n=t.n+1; t[t.n] = w end, 2) == "first second word"); assert(t[1] == "first" and t[2] == "second" and t[3] == nil)`},
		{"rev", `local function rev(s) return string.gsub(s, "(.)(.+)", function(c,s1) return rev(s1)..c end) end; local x = "abcdef"; assert(rev(rev(x)) == x)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewState()
			OpenLibraries(l)
			if err := LoadString(l, tt.code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			if err := l.ProtectedCall(0, 0, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
		})
	}
}

func TestIsolateForError(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	code := `
local ok, msg = pcall(load("for i = 1, 10, print do end"))
print("for-print msg: " .. tostring(msg))
assert(string.find(msg, "function", 1, true), "expected 'function' in: " .. msg)
`
	if err := LoadString(l, code); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	if err := l.ProtectedCall(0, 0, 0); err != nil {
		t.Fatalf("Error: %v", err)
	}
}

func TestIsolateErrorsDebug(t *testing.T) {
	tests := []struct {
		name string
		prog string
		msg  string
	}{
		{"global_bbbb", "aaa=1; bbbb=2; aaa=math.sin(3)+bbbb(3)", "global 'bbbb'"},
		{"method_bbbb", "aaa={}; do local aaa=1 end aaa:bbbb(3)", "method 'bbbb'"},
		{"field_bbbb", "local a={}; a.bbbb(3)", "field 'bbbb'"},
		{"number", "aaa={13}; local bbbb=1; aaa[bbbb](3)", "number"},
		{"concat_table", "aaa=(1)..{}", "a table value"},
		{"local_a", "local a; a(13)", "local 'a'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewState()
			OpenLibraries(l)
			code := "local f, msg = load([=[" + tt.prog + "]=])\n" +
				"if not f then return msg end\n" +
				"local ok, msg = pcall(f)\n" +
				"return (not ok) and msg\n"
			if err := LoadString(l, code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			if err := l.ProtectedCall(0, 1, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
			s, _ := l.ToString(-1)
			t.Logf("got: %q, want substring: %q", s, tt.msg)
			if s == "" || s == "false" {
				t.Errorf("no error for: %s", tt.prog)
			} else if !containsString(s, tt.msg) {
				t.Errorf("expected %q in error message %q", tt.msg, s)
			}
		})
	}
}

func containsString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestIsolateLineError(t *testing.T) {
	tests := []struct {
		name string
		code string
		line int
	}{
		{"for_string", "local a\n for i=1,'a' do \n print(i) \n end", 2},
		{"for_in_num", "\n local a \n for k,v in 3 \n do \n print(k) \n end", 3},
		{"for_in_num2", "\n\n for k,v in \n 3 \n do \n print(k) \n end", 4},
		{"func_field", "function a.x.y ()\na=a+1\nend", 1},
		{"arith_table", "a = \na\n+\n{}", 3},
		{"arith_div_print", "a = \n3\n+\n(\n4\n/\nprint)", 6},
		{"arith_print_add", "a = \nprint\n+\n(\n4\n/\n7)", 3},
		{"unary_minus", "a\n=\n-\n\nprint\n;", 3},
		{"call_line2", "a\n(\n23)", 2},
		{"field_call", "local a = {x = 13}\na\n.\nx\n(\n23\n)", 5},
		{"field_call2", "local a = {x = 13}\na\n.\nx\n(\n23 + a\n)", 6},
		{"error_str", "local b = false\nif not b then\n  error 'test'\nend", 3},
		{"error_str_nested", "local b = false\nif not b then\n  if not b then\n    if not b then\n      error 'test'\n    end\n  end\nend", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewState()
			OpenLibraries(l)
			code := "local function lineerror(s, l)\n" +
				"  local err, msg = pcall(load(s))\n" +
				"  local line = tonumber(string.match(msg, ':(%d+):'))\n" +
				"  return line, msg\n" +
				"end\n" +
				"return lineerror(...)"
			if err := LoadString(l, code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			l.PushString(tt.code)
			l.PushInteger(tt.line)
			if err := l.ProtectedCall(2, 2, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
			got, _ := l.ToInteger(-2)
			msg, _ := l.ToString(-1)
			t.Logf("expected line %d, got %d, msg: %s", tt.line, got, msg)
			if int(got) != tt.line {
				t.Errorf("expected line %d, got %d", tt.line, got)
			}
		})
	}
}

func TestIsolateErrorLevel(t *testing.T) {
	tests := []struct {
		name string
		xx   int
		line any // int or nil
	}{
		{"level3", 3, 3},
		{"level0", 0, nil},
		{"level1", 1, 2},
		{"level2", 2, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewState()
			OpenLibraries(l)
			prog := "  function g() f() end\n  function f(x) error('a', XX) end\ng()\n"
			code := fmt.Sprintf("XX=%d\n", tt.xx) +
				"local err, msg = pcall(load([=[" + prog + "]=]))\n" +
				"local line = tonumber(string.match(tostring(msg), ':(%d+):'))\n" +
				"return line, msg"
			if err := LoadString(l, code); err != nil {
				t.Fatalf("LoadString: %v", err)
			}
			if err := l.ProtectedCall(0, 2, 0); err != nil {
				t.Fatalf("Error: %v", err)
			}
			got, _ := l.ToInteger(-2)
			msg, _ := l.ToString(-1)
			if tt.line == nil {
				if got != 0 {
					t.Errorf("expected no line, got %d, msg: %s", got, msg)
				}
			} else {
				expected := tt.line.(int)
				t.Logf("expected line %d, got %d, msg: %s", expected, got, msg)
				if int(got) != expected {
					t.Errorf("expected line %d, got %d", expected, got)
				}
			}
		})
	}
}

func TestIsolateCallLine(t *testing.T) {
	l := NewState()
	OpenLibraries(l)
	code := "\n\t\t\tfunction barf()\n\t\t\t\ta = 3 + 2\n\t\t\t\tisNotDefined(\"Boom!\", a)\n\t\t\tend\n\t\t\tbarf()\n\t\t\t"
	if err := LoadString(l, code); err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	err := l.ProtectedCall(0, 0, 0)
	if err != nil {
		if !containsString(err.Error(), ":4:") {
			t.Errorf("expected :4: in error, got: %v", err)
		}
	}
}
