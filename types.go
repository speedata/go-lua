package lua

import (
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
)

type value interface{}
type float8 int

func debugValue(v value) string {
	switch v := v.(type) {
	case *table:
		entry := func(x value) string {
			if t, ok := x.(*table); ok {
				return fmt.Sprintf("table %#v", t)
			}
			return debugValue(x)
		}
		s := fmt.Sprintf("table %#v {[", v)
		for _, x := range v.array {
			s += entry(x) + ", "
		}
		s += "], {"
		for k, x := range v.hash {
			s += entry(k) + ": " + entry(x) + ", "
		}
		return s + "}}"
	case string:
		return "'" + v + "'"
	case float64:
		return fmt.Sprintf("%f", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case *luaClosure:
		return fmt.Sprintf("closure %s:%d %v", v.prototype.source, v.prototype.lineDefined, v)
	case *goClosure:
		return fmt.Sprintf("go closure %#v", v)
	case *goFunction:
		pc := reflect.ValueOf(v.Function).Pointer()
		f := runtime.FuncForPC(pc)
		file, line := f.FileLine(pc)
		return fmt.Sprintf("go function %s %s:%d", f.Name(), file, line)
	case *userData:
		return fmt.Sprintf("userdata %#v", v)
	case nil:
		return "nil"
	case bool:
		return fmt.Sprintf("%#v", v)
	}
	return fmt.Sprintf("unknown %#v %s", v, reflect.TypeOf(v).Name())
}

func stack(s []value) string {
	r := fmt.Sprintf("stack (len: %d, cap: %d):\n", len(s), cap(s))
	for i, v := range s {
		r = fmt.Sprintf("%s %d: %s\n", r, i, debugValue(v))
	}
	return r
}

func isFalse(s value) bool {
	if s == nil || s == none {
		return true
	}
	b, isBool := s.(bool)
	return isBool && !b
}

// isInteger returns true if the value is a Lua integer (int64).
func isInteger(v value) bool {
	_, ok := v.(int64)
	return ok
}

// isFloat returns true if the value is a Lua float (float64).
func isFloat(v value) bool {
	_, ok := v.(float64)
	return ok
}

// isNumber returns true if the value is a Lua number (int64 or float64).
func isNumber(v value) bool {
	switch v.(type) {
	case int64, float64:
		return true
	}
	return false
}

// toFloat converts a numeric value to float64.
// Returns the float value and true if successful.
func toFloat(v value) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// pow2_63 is 2^63 as float64, used for range checks.
// This is the smallest float64 that cannot be represented as int64.
const pow2_63Float = float64(1 << 63) // 9223372036854775808.0
const maxInt64 = int64(1<<63 - 1)     // 9223372036854775807
const minInt64 = int64(-1 << 63)      // -9223372036854775808

// toInteger converts a numeric value to int64.
// For float64, only succeeds if the value is integral and within int64 range.
// Returns the integer value and true if successful.
func toInteger(v value) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		// Check range first: valid int64 range is [-2^63, 2^63-1]
		// Due to float64 precision, n >= 2^63 means it's out of range
		if n >= pow2_63Float || n < -pow2_63Float {
			return 0, false
		}
		// Now safely convert and check round-trip
		if i := int64(n); float64(i) == n {
			return i, true
		}
	}
	return 0, false
}

// floatToInteger attempts to convert a float64 to int64.
// Returns the integer and true if the float represents an integer value
// that is within the valid int64 range.
func floatToInteger(f float64) (int64, bool) {
	// Check range first: valid int64 range is [-2^63, 2^63-1]
	if f >= pow2_63Float || f < -pow2_63Float {
		return 0, false
	}
	i := int64(f)
	if float64(i) == f {
		return i, true
	}
	return 0, false
}

// forLimit tries to convert a for-loop limit to an integer.
// This implements Lua 5.3 semantics where the limit can be a float
// that represents an integer value, or can be out of integer range
// (in which case we use MaxInt64 or MinInt64 as appropriate).
// Returns the integer limit and true if we can use an integer loop.
func forLimit(limitVal value, step int64) (int64, bool) {
	switch limit := limitVal.(type) {
	case int64:
		return limit, true
	case float64:
		// Try to convert float to integer
		if i, ok := floatToInteger(limit); ok {
			return i, true
		}
		// Float is out of integer range or not integral
		// If step > 0 and limit > MaxInt64, use MaxInt64
		// If step < 0 and limit < MinInt64, use MinInt64
		if step > 0 {
			if limit > 0 {
				// limit is larger than MaxInt64
				return maxInt64, true
			}
			// limit is smaller than MinInt64, loop won't run
			return minInt64, true
		} else {
			if limit < 0 {
				// limit is smaller than MinInt64
				return minInt64, true
			}
			// limit is larger than MaxInt64
			return maxInt64, true
		}
	}
	return 0, false
}

type localVariable struct {
	name           string
	startPC, endPC pc
}

type userData struct {
	metaTable, env *table
	data           interface{}
}

type upValueDesc struct {
	name    string
	isLocal bool
	index   int
}

type stackLocation struct {
	state *State
	index int
}

type prototype struct {
	constants                    []value
	code                         []instruction
	prototypes                   []prototype
	lineInfo                     []int32
	localVariables               []localVariable
	upValues                     []upValueDesc
	cache                        *luaClosure
	source                       string
	lineDefined, lastLineDefined int
	parameterCount, maxStackSize int
	isVarArg                     bool
}

func (p *prototype) upValueName(index int) string {
	if s := p.upValues[index].name; s != "" {
		return s
	}
	return "?"
}

func (p *prototype) lastLoad(reg int, lastPC pc) (loadPC pc, found bool) {
	var ip, jumpTarget pc
	for ; ip < lastPC; ip++ {
		i, maybe := p.code[ip], false
		switch i.opCode() {
		case opLoadNil:
			maybe = i.a() <= reg && reg <= i.a()+i.b()
		case opTForCall:
			maybe = reg >= i.a()+2
		case opCall, opTailCall:
			maybe = reg >= i.a()
		case opJump:
			if dest := ip + 1 + pc(i.sbx()); ip < dest && dest <= lastPC && dest > jumpTarget {
				jumpTarget = dest
			}
		case opTest:
			maybe = reg == i.a()
		default:
			maybe = testAMode(i.opCode()) && reg == i.a()
		}
		if maybe {
			if ip < jumpTarget { // Can't know loading instruction because code is conditional.
				found = false
			} else {
				loadPC, found = ip, true
			}
		}
	}
	return
}

func (p *prototype) objectName(reg int, lastPC pc) (name, kind string) {
	if name, isLocal := p.localName(reg+1, lastPC); isLocal {
		return name, "local"
	}
	if pc, found := p.lastLoad(reg, lastPC); found {
		i := p.code[pc]
		switch op := i.opCode(); op {
		case opMove:
			if b := i.b(); b < i.a() {
				return p.objectName(b, pc)
			}
		case opGetTableUp:
			name, kind = p.constantName(i.c(), pc), "local"
			if p.upValueName(i.b()) == "_ENV" {
				kind = "global"
			}
			return
		case opGetTable:
			name, kind = p.constantName(i.c(), pc), "local"
			if v, ok := p.localName(i.b()+1, pc); ok && v == "_ENV" {
				kind = "global"
			}
			return
		case opGetUpValue:
			return p.upValueName(i.b()), "upvalue"
		case opLoadConstant:
			if s, ok := p.constants[i.bx()].(string); ok {
				return s, "constant"
			}
		case opLoadConstantEx:
			if s, ok := p.constants[p.code[pc+1].ax()].(string); ok {
				return s, "constant"
			}
		case opSelf:
			return p.constantName(i.c(), pc), "method"
		}
	}
	return
}

func (p *prototype) constantName(k int, pc pc) string {
	if isConstant(k) {
		if s, ok := p.constants[constantIndex(k)].(string); ok {
			return s
		}
	} else if name, kind := p.objectName(k, pc); kind == "c" {
		return name
	}
	return "?"
}

func (p *prototype) localName(index int, pc pc) (string, bool) {
	for i := 0; i < len(p.localVariables) && p.localVariables[i].startPC <= pc; i++ {
		if pc < p.localVariables[i].endPC {
			if index--; index == 0 {
				return p.localVariables[i].name, true
			}
		}
	}
	return "", false
}

// Converts an integer to a "floating point byte", represented as
// (eeeeexxx), where the real value is (1xxx) * 2^(eeeee - 1) if
// eeeee != 0 and (xxx) otherwise.
func float8FromInt(x int) float8 {
	if x < 8 {
		return float8(x)
	}
	e := 0
	for ; x >= 0x10; e++ {
		x = (x + 1) >> 1
	}
	return float8(((e + 1) << 3) | (x - 8))
}

func intFromFloat8(x float8) int {
	e := x >> 3 & 0x1f
	if e == 0 {
		return int(x)
	}
	return int(x&7+8) << uint(e-1)
}

func arith(op Operator, v1, v2 float64) float64 {
	switch op {
	case OpAdd:
		return v1 + v2
	case OpSub:
		return v1 - v2
	case OpMul:
		return v1 * v2
	case OpDiv:
		return v1 / v2
	case OpMod:
		return v1 - math.Floor(v1/v2)*v2
	case OpPow:
		// Golang bug: math.Pow(10.0, 33.0) is incorrect by 1 bit.
		if v1 == 10.0 && float64(int(v2)) == v2 {
			return math.Pow10(int(v2))
		}
		return math.Pow(v1, v2)
	case OpUnaryMinus:
		return -v1
	}
	panic(fmt.Sprintf("not an arithmetic op code (%d)", op))
}

func (l *State) parseNumber(s string) (v float64, ok bool) { // TODO this is f*cking ugly - scanner.readNumber should be refactored.
	if len(strings.Fields(s)) != 1 || strings.ContainsRune(s, 0) {
		return
	}
	scanner := scanner{l: l, r: strings.NewReader(s)}
	t := scanner.scan()

	// Helper to extract numeric value from token
	getNumber := func(tok token) (float64, bool) {
		switch tok.t {
		case tkNumber:
			return tok.n, true
		case tkInteger:
			return float64(tok.i), true
		default:
			return 0, false
		}
	}

	if t.t == '-' {
		t = scanner.scan()
		if n, numOk := getNumber(t); numOk {
			v, ok = -n, true
		}
	} else if n, isNum := getNumber(t); isNum {
		v, ok = n, true
	} else if t.t == '+' {
		t = scanner.scan()
		if n, numOk := getNumber(t); numOk {
			v, ok = n, true
		}
	}
	if ok && scanner.scan().t != tkEOS {
		ok = false
	} else if math.IsInf(v, 0) || math.IsNaN(v) {
		ok = false
	}
	return
}

func (l *State) toNumber(r value) (v float64, ok bool) {
	if v, ok = r.(float64); ok {
		return
	}
	if i, isInt := r.(int64); isInt {
		return float64(i), true
	}
	var s string
	if s, ok = r.(string); ok {
		if err := l.protectedCall(func() { v, ok = l.parseNumber(strings.TrimSpace(s)) }, l.top, l.errorFunction); err != nil {
			l.pop() // Remove error message from the stack.
			ok = false
		}
	}
	return
}

func (l *State) toString(index int) (s string, ok bool) {
	if s, ok = toString(l.stack[index]); ok {
		l.stack[index] = s
	}
	return
}

func numberToString(f float64) string {
	return fmt.Sprintf("%.14g", f)
}

func integerToString(i int64) string {
	return fmt.Sprintf("%d", i)
}

func toString(r value) (string, bool) {
	switch r := r.(type) {
	case string:
		return r, true
	case float64:
		return numberToString(r), true
	case int64:
		return integerToString(r), true
	}
	return "", false
}

func pairAsNumbers(p1, p2 value) (f1, f2 float64, ok bool) {
	f1, ok = toFloat(p1)
	if !ok {
		return
	}
	f2, ok = toFloat(p2)
	return
}

func pairAsStrings(p1, p2 value) (s1, s2 string, ok bool) {
	if s1, ok = p1.(string); !ok {
		return
	}
	s2, ok = p2.(string)
	return
}
