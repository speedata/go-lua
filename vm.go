package lua

import (
	"fmt"
	"math"
	"strings"
)

// numericValues extracts float64 values from two operands.
// Handles both float64 and int64 types for Lua 5.3 compatibility.
func numericValues(b, c value) (nb, nc float64, ok bool) {
	nb, ok = toFloat(b)
	if !ok {
		return
	}
	nc, ok = toFloat(c)
	return
}

// integerValues extracts int64 values from two operands.
// Returns true only if BOTH operands are actual int64 values (not floats).
// This matches Lua 5.3 semantics: float + float = float, even if values are integral.
func integerValues(b, c value) (ib, ic int64, ok bool) {
	ib, ok = b.(int64)
	if !ok {
		return
	}
	ic, ok = c.(int64)
	return
}

// coerceToIntegers attempts to convert both operands to int64 for bitwise operations.
// Floats with exact integer representations are converted, and strings are coerced
// to numbers first. This matches Lua 5.3 bitwise operation semantics.
func (l *State) coerceToIntegers(b, c value) (ib, ic int64, ok bool) {
	ib, ok = l.toIntegerString(b)
	if !ok {
		return
	}
	ic, ok = l.toIntegerString(c)
	return
}

// valueTypeName returns the Lua type name of a Go value,
// checking __name in the metatable for tables and userdata.
func (l *State) valueTypeName(v value) string {
	switch val := v.(type) {
	case nil:
		return "nil"
	case bool:
		return "boolean"
	case int64:
		return "number"
	case float64:
		return "number"
	case string:
		return "string"
	case *table:
		if val.metaTable != nil {
			if s, ok := val.metaTable.atString("__name").(string); ok {
				return s
			}
		}
		return "table"
	case *luaClosure, *goClosure, *goFunction:
		return "function"
	case *userData:
		if val.metaTable != nil {
			if s, ok := val.metaTable.atString("__name").(string); ok {
				return s
			}
		}
		return "userdata"
	default:
		return "no value"
	}
}

// intIDiv performs integer floor division (Lua 5.4 // operator).
// Returns floor(a/b), handling negative numbers correctly.
// Caller must ensure n != 0.
func intIDiv(m, n int64) int64 {
	q := m / n
	// Adjust for floor division when signs differ
	if (m^n) < 0 && m%n != 0 {
		q--
	}
	return q
}

// intMod performs integer modulo (Lua 5.4 % operator).
// Uses the definition: a % b == a - (a // b) * b
// Caller must ensure n != 0.
func intMod(m, n int64) int64 {
	return m - intIDiv(m, n)*n
}

// intShiftLeft performs a left shift operation.
// If y is negative, performs right shift instead.
// Lua 5.3 shift semantics: shifts >= 64 bits result in 0.
func intShiftLeft(x, y int64) int64 {
	if y >= 64 {
		return 0
	} else if y >= 0 {
		return x << uint(y)
	} else if y > -64 {
		return int64(uint64(x) >> uint(-y))
	}
	return 0
}

// tmToOperator maps tagMethod to Operator for arithmetic operations
var tmToOperator = map[tm]Operator{
	tmAdd:        OpAdd,
	tmSub:        OpSub,
	tmMul:        OpMul,
	tmDiv:        OpDiv,
	tmMod:        OpMod,
	tmPow:        OpPow,
	tmUnaryMinus: OpUnaryMinus,
}

func (l *State) arith(rb, rc value, op tm) value {
	if b, ok := l.toNumber(rb); ok {
		if c, ok := l.toNumber(rc); ok {
			if operator, ok := tmToOperator[op]; ok {
				return arith(operator, b, c)
			}
		}
	}
	if result, ok := l.callBinaryTagMethod(rb, rc, op); ok {
		return result
	}
	l.arithError(rb, rc)
	return nil
}

// bitwiseArith handles bitwise operations, trying metamethods first before
// producing the appropriate error message for non-integer floats.
func (l *State) bitwiseArith(rb, rc value, op tm) value {
	// Try metamethods first
	if result, ok := l.callBinaryTagMethod(rb, rc, op); ok {
		return result
	}
	// No metamethod - produce appropriate error
	l.bitwiseError(rb, rc)
	return nil
}

// arithOrBitwise dispatches to either arith or bitwiseArith based on the
// tag method type. Binary MMBIN opcodes need this to produce correct error
// messages ("bitwise operation" vs "arithmetic").
func (l *State) arithOrBitwise(rb, rc value, op tm) value {
	switch op {
	case tmBAnd, tmBOr, tmBXor, tmShl, tmShr:
		return l.bitwiseArith(rb, rc, op)
	default:
		return l.arith(rb, rc, op)
	}
}

func (l *State) tableAt(t value, key value) value {
	for loop := 0; loop < maxTagLoop; loop++ {
		var tm value
		if table, ok := t.(*table); ok {
			if result := table.at(key); result != nil {
				return result
			} else if tm = l.fastTagMethod(table.metaTable, tmIndex); tm == nil {
				return nil
			}
		} else if tm = l.tagMethodByObject(t, tmIndex); tm == nil {
			l.typeError(t, "index")
		}
		switch tm.(type) {
		case closure, *goFunction:
			return l.callTagMethod(tm, t, key)
		}
		t = tm
	}
	l.runtimeError("loop in table")
	return nil
}

func (l *State) setTableAt(t value, key value, val value) {
	for loop := 0; loop < maxTagLoop; loop++ {
		var tm value
		if table, ok := t.(*table); ok {
			if table.tryPut(l, key, val) {
				// previous non-nil value ==> metamethod irrelevant
				table.invalidateTagMethodCache()
				return
			} else if tm = l.fastTagMethod(table.metaTable, tmNewIndex); tm == nil {
				// no metamethod
				table.put(l, key, val)
				table.invalidateTagMethodCache()
				return
			}
		} else if tm = l.tagMethodByObject(t, tmNewIndex); tm == nil {
			l.typeError(t, "index")
		}
		switch tm.(type) {
		case closure, *goFunction:
			l.callTagMethodV(tm, t, key, val)
			return
		}
		t = tm
	}
	l.runtimeError("loop in setTable")
}

func (l *State) objectLength(v value) value {
	var tm value
	switch v := v.(type) {
	case *table:
		if tm = l.fastTagMethod(v.metaTable, tmLen); tm == nil {
			return float64(v.length())
		}
	case string:
		return float64(len(v))
	default:
		if tm = l.tagMethodByObject(v, tmLen); tm == nil {
			l.typeError(v, "get length of")
		}
	}
	return l.callTagMethod(tm, v, v)
}

func (l *State) equalTagMethod(mt1, mt2 *table, event tm) value {
	if tm1 := l.fastTagMethod(mt1, event); tm1 == nil { // no metamethod
	} else if mt1 == mt2 { // same metatables => same metamethods
		return tm1
	} else if tm2 := l.fastTagMethod(mt2, event); tm2 == nil { // no metamethod
	} else if tm1 == tm2 { // same metamethods
		return tm1
	}
	return nil
}

func (l *State) equalObjects(t1, t2 value) bool {
	var tm value
	switch t1 := t1.(type) {
	case *userData:
		if t1 == t2 {
			return true
		} else if t2, ok := t2.(*userData); ok {
			// Lua 5.3: try __eq from t1's metatable first, then t2's
			tm = l.fastTagMethod(t1.metaTable, tmEq)
			if tm == nil {
				tm = l.fastTagMethod(t2.metaTable, tmEq)
			}
		}
	case *table:
		if t1 == t2 {
			return true
		} else if t2, ok := t2.(*table); ok {
			// Lua 5.3: try __eq from t1's metatable first, then t2's
			tm = l.fastTagMethod(t1.metaTable, tmEq)
			if tm == nil {
				tm = l.fastTagMethod(t2.metaTable, tmEq)
			}
		}
	case int64:
		// Lua 5.3: compare int with float carefully to preserve precision
		switch t2 := t2.(type) {
		case int64:
			return t1 == t2
		case float64:
			// Check if float has exact integer representation
			if i2 := int64(t2); float64(i2) == t2 {
				// Float is exact integer, compare as integers
				return t1 == i2
			}
			// Float is not exact integer, convert int to float
			return float64(t1) == t2
		}
		return false
	case float64:
		// Lua 5.3: compare float with int carefully to preserve precision
		switch t2 := t2.(type) {
		case float64:
			return t1 == t2
		case int64:
			// Check if float has exact integer representation
			if i1 := int64(t1); float64(i1) == t1 {
				// Float is exact integer, compare as integers
				return i1 == t2
			}
			// Float is not exact integer, convert int to float
			return t1 == float64(t2)
		}
		return false
	default:
		return t1 == t2
	}
	return tm != nil && !isFalse(l.callTagMethod(tm, t1, t2))
}

func (l *State) callBinaryTagMethod(p1, p2 value, event tm) (value, bool) {
	tm := l.tagMethodByObject(p1, event)
	if tm == nil {
		tm = l.tagMethodByObject(p2, event)
	}
	if tm == nil {
		return nil, false
	}
	return l.callTagMethod(tm, p1, p2), true
}

func (l *State) callOrderTagMethod(left, right value, event tm) (bool, bool) {
	result, ok := l.callBinaryTagMethod(left, right, event)
	return !isFalse(result), ok
}

func (l *State) lessThan(left, right value) bool {
	// Lua 5.3: compare numbers carefully to preserve precision
	switch li := left.(type) {
	case int64:
		switch ri := right.(type) {
		case int64:
			return li < ri
		case float64:
			// Compare int < float
			return intLessFloat(li, ri)
		}
	case float64:
		switch ri := right.(type) {
		case float64:
			return li < ri
		case int64:
			// Compare float < int
			return floatLessInt(li, ri)
		}
	}
	if ls, ok := left.(string); ok {
		if rs, ok := right.(string); ok {
			return ls < rs
		}
	}
	if result, ok := l.callOrderTagMethod(left, right, tmLT); ok {
		return result
	}
	l.orderError(left, right)
	return false
}

// pow2_63 is 2^63, the boundary between int64 representable and not
const pow2_63 float64 = 9223372036854775808.0 // 2^63

// intLessFloat compares int64 < float64 with proper precision handling
func intLessFloat(i int64, f float64) bool {
	if math.IsNaN(f) {
		return false // NaN comparisons always false
	}
	// Check if float is outside int64 range
	if f >= pow2_63 { // f >= 2^63, definitely > any int64
		return true
	}
	if f < float64(math.MinInt64) { // f < -2^63, definitely < any int64
		return false
	}
	// Float is within int64 range
	fi := int64(f)
	if float64(fi) == f {
		// Exact conversion
		return i < fi
	}
	// Float is not exact integer, but within range
	// f is between fi and fi+1 (for positive) or fi-1 and fi (for negative)
	// i < f is true if i <= fi (since f > fi for positive fractional parts)
	if f > 0 {
		return i <= fi
	}
	// For negative non-integers, f is between fi-1 and fi
	// i < f means i < fi (since f < fi)
	return i < fi
}

// floatLessInt compares float64 < int64 with proper precision handling
func floatLessInt(f float64, i int64) bool {
	if math.IsNaN(f) {
		return false // NaN comparisons always false
	}
	// Check if float is outside int64 range
	if f >= pow2_63 { // f >= 2^63, definitely > any int64
		return false
	}
	if f < float64(math.MinInt64) { // f < -2^63, definitely < any int64
		return true
	}
	// Float is within int64 range
	fi := int64(f)
	if float64(fi) == f {
		// Exact conversion
		return fi < i
	}
	// Float is not exact integer
	if f > 0 {
		// f is between fi and fi+1
		// f < i means fi+1 <= i, i.e., fi < i
		return fi < i
	}
	// For negative non-integers, f is between fi-1 and fi
	// f < i means fi <= i
	return fi <= i
}

func (l *State) lessOrEqual(left, right value) bool {
	// Lua 5.3: compare numbers carefully to preserve precision
	switch li := left.(type) {
	case int64:
		switch ri := right.(type) {
		case int64:
			return li <= ri
		case float64:
			return intLessOrEqualFloat(li, ri)
		}
	case float64:
		switch ri := right.(type) {
		case float64:
			return li <= ri
		case int64:
			return floatLessOrEqualInt(li, ri)
		}
	}
	if ls, ok := left.(string); ok {
		if rs, ok := right.(string); ok {
			return ls <= rs
		}
	}
	if result, ok := l.callOrderTagMethod(left, right, tmLE); ok {
		return result
	}
	// Fall back to "not (b < a)" using __lt.
	// Set callStatusLEQ so finishOp knows to negate the result after yield.
	l.callInfo.setCallStatus(callStatusLEQ)
	if result, ok := l.callOrderTagMethod(right, left, tmLT); ok {
		l.callInfo.clearCallStatus(callStatusLEQ)
		return !result
	}
	l.orderError(left, right)
	return false
}

// intLessOrEqualFloat compares int64 <= float64 with proper precision handling
func intLessOrEqualFloat(i int64, f float64) bool {
	if math.IsNaN(f) {
		return false
	}
	// Check if float is outside int64 range
	if f >= pow2_63 { // f >= 2^63, definitely > any int64
		return true
	}
	if f < float64(math.MinInt64) { // f < -2^63, definitely < any int64
		return false
	}
	// Float is within int64 range
	fi := int64(f)
	if float64(fi) == f {
		// Exact conversion
		return i <= fi
	}
	// Float is not exact integer
	if f > 0 {
		// f is between fi and fi+1
		// i <= f means i <= fi (since fi < f)
		return i <= fi
	}
	// For negative non-integers, f is between fi-1 and fi
	// i <= f means i <= fi-1, i.e., i < fi
	return i < fi
}

// floatLessOrEqualInt compares float64 <= int64 with proper precision handling
func floatLessOrEqualInt(f float64, i int64) bool {
	if math.IsNaN(f) {
		return false
	}
	// Check if float is outside int64 range
	if f >= pow2_63 { // f >= 2^63, definitely > any int64
		return false
	}
	if f < float64(math.MinInt64) { // f < -2^63, definitely < any int64
		return true
	}
	// Float is within int64 range
	fi := int64(f)
	if float64(fi) == f {
		// Exact conversion
		return fi <= i
	}
	// Float is not exact integer
	if f > 0 {
		// f is between fi and fi+1
		// f <= i means fi+1 <= i, i.e., fi < i
		return fi < i
	}
	// For negative non-integers, f is between fi-1 and fi
	// f <= i means fi <= i
	return fi <= i
}

func (l *State) concat(total int) {
	t := func(i int) value { return l.stack[l.top-i] }
	put := func(i int, v value) { l.stack[l.top-i] = v }
	concatTagMethod := func() {
		if v, ok := l.callBinaryTagMethod(t(2), t(1), tmConcat); !ok {
			l.concatError(t(2), t(1))
		} else {
			put(2, v)
		}
	}
	l.assert(total >= 2)
	for total > 1 {
		n := 2 // # of elements handled in this pass (at least 2)
		s2, ok := t(2).(string)
		if !ok {
			_, ok = t(2).(float64)
		}
		if !ok {
			_, ok = t(2).(int64)
		}
		if !ok {
			concatTagMethod()
		} else if s1, ok := l.toString(l.top - 1); !ok {
			concatTagMethod()
		} else if len(s1) == 0 {
			v, _ := l.toString(l.top - 2)
			put(2, v)
		} else if s2, ok = t(2).(string); ok && len(s2) == 0 {
			put(2, t(1))
		} else {
			// at least 2 non-empty strings; scarf as many as possible
			ss := []string{s1}
			for ; n <= total; n++ {
				if s, ok := l.toString(l.top - n); ok {
					ss = append(ss, s)
				} else {
					break
				}
			}
			n-- // last increment wasn't valid
			for i, j := 0, len(ss)-1; i < j; i, j = i+1, j-1 {
				ss[i], ss[j] = ss[j], ss[i]
			}
			put(len(ss), strings.Join(ss, ""))
		}
		total -= n - 1 // created 1 new string from `n` strings
		l.top -= n - 1 // popped `n` strings and pushed 1
	}
}

// maxIWTHABS is the maximum interval without absolute line info.
const maxIWTHABS = 128

// getBaseline finds the baseline (pc, line) for a given instruction PC using absLineInfos.
func getBaseline(p *prototype, pc int) (int, int) {
	if len(p.absLineInfos) == 0 || pc < p.absLineInfos[0].pc {
		return -1, p.lineDefined
	}
	// Binary search
	lo, hi := 0, len(p.absLineInfos)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if p.absLineInfos[mid].pc <= pc {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return p.absLineInfos[lo].pc, p.absLineInfos[lo].line
}

// getFuncLine resolves a PC to a line number using Lua 5.4 split lineinfo.
func getFuncLine(p *prototype, pc int) int {
	if len(p.lineInfo) == 0 {
		return -1
	}
	basePC, baseLine := getBaseline(p, pc)
	for basePC < pc {
		basePC++
		baseLine += int(p.lineInfo[basePC])
	}
	return baseLine
}

func (l *State) traceExecution() {
	callInfo := l.callInfo
	// For vararg functions, skip tracing during VARARGPREP instruction.
	// Matches C Lua where trap=0 during VARARGPREP; hooks start after it.
	if callInfo.savedPC == 0 {
		if p := l.prototype(callInfo); p.isVarArg {
			return
		}
	}
	mask := l.hookMask
	countHook := mask&MaskCount != 0 && l.hookCount == 0
	if countHook {
		l.resetHookCount()
	}
	if callInfo.isCallStatus(callStatusHookYielded) {
		callInfo.clearCallStatus(callStatusHookYielded)
		return
	}
	if countHook {
		l.hook(HookCount, -1)
	}
	if mask&MaskLine != 0 {
		p := l.prototype(callInfo)
		npc := callInfo.savedPC // index of instruction about to execute
		newline := getFuncLine(p, int(npc))
		// L->oldpc may be invalid; use zero in this case (matches C Lua)
		oldpc := l.oldPC
		if int(oldpc) >= len(p.code) {
			oldpc = 0
		}
		if callInfo.savedPC <= oldpc || newline != getFuncLine(p, int(oldpc)) {
			l.hook(HookLine, newline)
		}
	}
	l.oldPC = callInfo.savedPC
	if l.shouldYield {
		if countHook {
			l.hookCount = 1
		}
		callInfo.savedPC--
		callInfo.setCallStatus(callStatusHookYielded)
		callInfo.function = l.top - 1
		l.Yield(0)
	}
}

// rkc returns constants[C] if the k-bit is set, else frame[C].
// Used by SET opcodes where the value can be a constant or register.
func rkc(i instruction, constants []value, frame []value) value {
	if i.k() != 0 {
		return constants[i.c()]
	}
	return frame[i.c()]
}

// forLimit54 converts the for-loop limit to integer and checks if the loop should be skipped.
// This implements Lua 5.4's forlimit() function.
// For positive step, the limit is floored; for negative step, it is ceiled.
// This matches C Lua's use of F2Ifloor/F2Iceil in luaV_tointeger.
func (l *State) forLimit54(limitVal value, init, step int64) (int64, bool) {
	switch limit := limitVal.(type) {
	case int64:
		if step > 0 {
			return limit, init > limit
		}
		return limit, init < limit
	case float64:
		// Convert float limit to integer using floor (step>0) or ceil (step<0).
		// This matches C Lua's forlimit which uses F2Ifloor/F2Iceil.
		var iLimit int64
		if step < 0 {
			iLimit = int64(math.Ceil(limit))
		} else {
			iLimit = int64(math.Floor(limit))
		}
		// Check if the conversion is within integer range
		if limit >= float64(minInt64) && limit <= float64(maxInt64) {
			if step > 0 {
				return iLimit, init > iLimit
			}
			return iLimit, init < iLimit
		}
		// Float is out of integer range
		if limit > 0 {
			if step < 0 {
				return 0, true // positive limit out of range with descending step → skip
			}
			return maxInt64, init > maxInt64
		}
		if step > 0 {
			return 0, true // negative limit out of range with ascending step → skip
		}
		return minInt64, init < minInt64
	case string:
		if f, ok := l.toNumber(limit); ok {
			return l.forLimit54(f, init, step)
		}
	}
	l.runtimeError(fmt.Sprintf("bad 'for' limit (number expected, got %s)", l.valueTypeName(limitVal)))
	return 0, true
}

// callOrderImmediate calls order metamethods for immediate comparison opcodes.
// flip=true means the arguments are swapped (for GTI/GEI).
func (l *State) callOrderImmediate(ra value, imm int, flip bool, isFloat bool, event tm) bool {
	var p2 value
	if isFloat {
		p2 = float64(imm)
	} else {
		p2 = int64(imm)
	}
	if flip {
		result, ok := l.callOrderTagMethod(p2, ra, event)
		if !ok {
			l.orderError(p2, ra)
		}
		return result
	}
	result, ok := l.callOrderTagMethod(ra, p2, event)
	if !ok {
		l.orderError(ra, p2)
	}
	return result
}

// luaMod computes Lua's float modulo: a - floor(a/b)*b
func luaMod(a, b float64) float64 {
	r := math.Mod(a, b)
	if r != 0 && (r > 0) != (b > 0) {
		r += b
	}
	return r
}

func clear(r []value) {
	for i := range r {
		r[i] = nil
	}
}

func (l *State) execute() { l.executeSwitch() }

func newFrame(l *State, ci *callInfo) (frame []value, closure *luaClosure, constants []value) {
	// TODO l.assert(ci == l.callInfo)
	frame = ci.frame
	closure, _ = l.stack[ci.function].(*luaClosure)
	constants = closure.prototype.constants
	return
}

func expectNext(ci *callInfo, expected opCode) instruction {
	i := ci.step() // go to next instruction
	if op := i.opCode(); op != expected {
		panic(fmt.Sprintf("expected opcode %s, got %s", opNames[expected], opNames[op]))
	}
	return i
}


func (l *State) executeSwitch() {
	ci := l.callInfo
	frame, closure, constants := newFrame(l, ci)
	for {
		if l.hookMask&(MaskLine|MaskCount) != 0 {
			if l.hookCount--; l.hookCount == 0 || l.hookMask&MaskLine != 0 {
				l.traceExecution()
				frame = ci.frame
			}
		}
		switch i := ci.step(); i.opCode() {
		case opMove:
			frame[i.a()] = frame[i.b()]

		case opLoadI:
			frame[i.a()] = int64(i.sbx())

		case opLoadF:
			frame[i.a()] = float64(i.sbx())

		case opLoadConstant:
			frame[i.a()] = constants[i.bx()]

		case opLoadConstantEx:
			frame[i.a()] = constants[expectNext(ci, opExtraArg).ax()]

		case opLoadFalse:
			frame[i.a()] = false

		case opLoadFalseSkip:
			frame[i.a()] = false
			ci.skip()

		case opLoadTrue:
			frame[i.a()] = true

		case opLoadNil:
			a, b := i.a(), i.b()
			clear(frame[a : a+b+1])

		case opGetUpValue:
			frame[i.a()] = closure.upValue(i.b())

		case opSetUpValue:
			closure.setUpValue(i.b(), frame[i.a()])

		case opGetTableUp:
			tmp := l.tableAt(closure.upValue(i.b()), constants[i.c()])
			frame = ci.frame
			frame[i.a()] = tmp

		case opGetTable:
			tmp := l.tableAt(frame[i.b()], frame[i.c()])
			frame = ci.frame
			frame[i.a()] = tmp

		case opGetI:
			tmp := l.tableAt(frame[i.b()], int64(i.c()))
			frame = ci.frame
			frame[i.a()] = tmp

		case opGetField:
			tmp := l.tableAt(frame[i.b()], constants[i.c()])
			frame = ci.frame
			frame[i.a()] = tmp

		case opSetTableUp:
			l.setTableAt(closure.upValue(i.a()), constants[i.b()], rkc(i, constants, frame))
			frame = ci.frame

		case opSetTable:
			l.setTableAt(frame[i.a()], frame[i.b()], rkc(i, constants, frame))
			frame = ci.frame

		case opSetI:
			l.setTableAt(frame[i.a()], int64(i.b()), rkc(i, constants, frame))
			frame = ci.frame

		case opSetField:
			l.setTableAt(frame[i.a()], constants[i.b()], rkc(i, constants, frame))
			frame = ci.frame

		case opNewTable:
			a := i.a()
			b := i.b() // log2(hash size) + 1
			c := i.c() // array size
			if i.k() != 0 {
				c += expectNext(ci, opExtraArg).ax() * (maxArgC + 1)
			} else {
				ci.skip() // skip extra arg (which is 0)
			}
			hashSize := 0
			if b > 0 {
				hashSize = 1 << (b - 1)
			}
			if hashSize != 0 || c != 0 {
				frame[a] = newTableWithSize(c, hashSize)
			} else {
				frame[a] = newTable()
			}

		case opSelf:
			a := i.a()
			rb := frame[i.b()]
			rc := rkc(i, constants, frame)
			tmp := l.tableAt(rb, rc)
			frame = ci.frame
			frame[a+1] = rb
			frame[a] = tmp

		// --- Arithmetic with immediate (sC) ---
		case opAddI:
			b := frame[i.b()]
			ic := int64(i.sC())
			if ib, ok := b.(int64); ok {
				frame[i.a()] = ib + ic
				ci.skip()
				break
			}
			if nb, ok := toFloat(b); ok {
				frame[i.a()] = nb + float64(ic)
				ci.skip()
				break
			}
			// fall through to MMBINI

		// --- Arithmetic with constant (K[C]) ---
		case opAddK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				frame[i.a()] = ib + ic
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb + nc
				ci.skip()
				break
			}

		case opSubK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				frame[i.a()] = ib - ic
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb - nc
				ci.skip()
				break
			}

		case opMulK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				frame[i.a()] = ib * ic
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb * nc
				ci.skip()
				break
			}

		case opModK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				if ic == 0 {
					l.runtimeError("attempt to perform 'n%0'")
				}
				frame[i.a()] = intMod(ib, ic)
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = luaMod(nb, nc)
				ci.skip()
				break
			}

		case opPowK:
			b, c := frame[i.b()], constants[i.c()]
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = math.Pow(nb, nc)
				ci.skip()
				break
			}

		case opDivK:
			b, c := frame[i.b()], constants[i.c()]
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb / nc
				ci.skip()
				break
			}

		case opIDivK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				if ic == 0 {
					l.runtimeError("attempt to divide by zero")
				}
				frame[i.a()] = intIDiv(ib, ic)
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = math.Floor(nb / nc)
				ci.skip()
				break
			}

		case opBAndK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = ib & ic
					ci.skip()
					break
				}
			}

		case opBOrK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = ib | ic
					ci.skip()
					break
				}
			}

		case opBXorK:
			b, c := frame[i.b()], constants[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = ib ^ ic
					ci.skip()
					break
				}
			}

		// --- Shift with immediate ---
		case opShrI:
			// R[A] := R[B] >> sC
			b := frame[i.b()]
			if ib, ok := toInteger(b); ok {
				frame[i.a()] = intShiftLeft(ib, -int64(i.sC()))
				ci.skip()
				break
			}

		case opShlI:
			// R[A] := sC << R[B] (sC is value, R[B] is shift amount)
			b := frame[i.b()]
			if ib, ok := toInteger(b); ok {
				frame[i.a()] = intShiftLeft(int64(i.sC()), ib)
				ci.skip()
				break
			}

		// --- Register-register arithmetic ---
		case opAdd:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				frame[i.a()] = ib + ic
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb + nc
				ci.skip()
				break
			}

		case opSub:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				frame[i.a()] = ib - ic
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb - nc
				ci.skip()
				break
			}

		case opMul:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				frame[i.a()] = ib * ic
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb * nc
				ci.skip()
				break
			}

		case opMod:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				if ic == 0 {
					l.runtimeError("attempt to perform 'n%0'")
				}
				frame[i.a()] = intMod(ib, ic)
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = luaMod(nb, nc)
				ci.skip()
				break
			}

		case opPow:
			b, c := frame[i.b()], frame[i.c()]
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = math.Pow(nb, nc)
				ci.skip()
				break
			}

		case opDiv:
			b, c := frame[i.b()], frame[i.c()]
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = nb / nc
				ci.skip()
				break
			}

		case opIDiv:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ic, ok := integerValues(b, c); ok {
				if ic == 0 {
					l.runtimeError("attempt to divide by zero")
				}
				frame[i.a()] = intIDiv(ib, ic)
				ci.skip()
				break
			}
			if nb, nc, ok := numericValues(b, c); ok {
				frame[i.a()] = math.Floor(nb / nc)
				ci.skip()
				break
			}

		case opBAnd:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = ib & ic
					ci.skip()
					break
				}
			}

		case opBOr:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = ib | ic
					ci.skip()
					break
				}
			}

		case opBXor:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = ib ^ ic
					ci.skip()
					break
				}
			}

		case opShl:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = intShiftLeft(ib, ic)
					ci.skip()
					break
				}
			}

		case opShr:
			b, c := frame[i.b()], frame[i.c()]
			if ib, ok := toInteger(b); ok {
				if ic, ok := toInteger(c); ok {
					frame[i.a()] = intShiftLeft(ib, -ic)
					ci.skip()
					break
				}
			}

		// --- MMBIN metamethod fallbacks ---
		case opMMBin:
			pi := ci.code[ci.savedPC-2]
			ra, rb := frame[i.a()], frame[i.b()]
			event := tm(i.c())
			result := l.arithOrBitwise(ra, rb, event)
			frame = ci.frame
			frame[pi.a()] = result

		case opMMBinI:
			pi := ci.code[ci.savedPC-2]
			ra := frame[i.a()]
			imm := int64(i.sB())
			event := tm(i.c())
			if i.k() != 0 {
				result := l.arithOrBitwise(imm, ra, event)
				frame = ci.frame
				frame[pi.a()] = result
			} else {
				result := l.arithOrBitwise(ra, imm, event)
				frame = ci.frame
				frame[pi.a()] = result
			}

		case opMMBinK:
			pi := ci.code[ci.savedPC-2]
			ra := frame[i.a()]
			kb := constants[i.b()]
			event := tm(i.c())
			if i.k() != 0 {
				result := l.arithOrBitwise(kb, ra, event)
				frame = ci.frame
				frame[pi.a()] = result
			} else {
				result := l.arithOrBitwise(ra, kb, event)
				frame = ci.frame
				frame[pi.a()] = result
			}

		// --- Unary operations ---
		case opUnaryMinus:
			b := frame[i.b()]
			if ib, ok := b.(int64); ok {
				frame[i.a()] = -ib
			} else if nb, ok := toFloat(b); ok {
				frame[i.a()] = -nb
			} else {
				tmp := l.arith(b, b, tmUnaryMinus)
				frame = ci.frame
				frame[i.a()] = tmp
			}

		case opBNot:
			b := frame[i.b()]
			if ib, ok := toInteger(b); ok {
				frame[i.a()] = ^ib
			} else {
				tmp := l.bitwiseArith(b, b, tmBNot)
				frame = ci.frame
				frame[i.a()] = tmp
			}

		case opNot:
			frame[i.a()] = isFalse(frame[i.b()])

		case opLength:
			tmp := l.objectLength(frame[i.b()])
			frame = ci.frame
			frame[i.a()] = tmp

		// --- Concat (5.4: R[A]..R[A+B-1], B values, result in R[A]) ---
		case opConcat:
			a := i.a()
			n := i.b()
			l.top = ci.stackIndex(a + n)
			l.concat(n)
			frame = ci.frame
			frame[a] = l.stack[l.top-1]
			l.top = ci.top

		// --- Close / TBC ---
		case opClose:
			l.closeYieldable(ci.stackIndex(i.a()))

		case opTBC:
			ra := ci.stackIndex(i.a())
			v := l.stack[ra]
			// false/nil don't need closing
			if v != nil && v != false {
				// Check for __close metamethod
				if l.tagMethodByObject(v, tmClose) == nil {
					// Try to get the variable name for the error message
					p := l.stack[ci.function].(*luaClosure).prototype
					vname := "?"
					if name, found := p.localName(i.a()+1, pc(ci.savedPC-1)); found {
						vname = name
					}
					l.runtimeError(fmt.Sprintf("variable '%s' got a non-closable value", vname))
				}
				l.newTBCUpValue(ra)
			}

		// --- Jump (5.4: isJ format, sJ signed offset) ---
		case opJump:
			ci.jump(i.sJ())

		// --- Comparisons (5.4: k-bit for expected condition, followed by JMP) ---
		case opEqual:
			cond := l.equalObjects(frame[i.a()], frame[i.b()])
			frame = ci.frame
			doCondJump(ci, cond, i.k() != 0)

		case opLessThan:
			cond := l.lessThan(frame[i.a()], frame[i.b()])
			frame = ci.frame
			doCondJump(ci, cond, i.k() != 0)

		case opLessOrEqual:
			cond := l.lessOrEqual(frame[i.a()], frame[i.b()])
			frame = ci.frame
			doCondJump(ci, cond, i.k() != 0)

		case opEqualK:
			cond := l.equalObjects(frame[i.a()], constants[i.b()])
			frame = ci.frame
			doCondJump(ci, cond, i.k() != 0)

		case opEqualI:
			ra := frame[i.a()]
			imm := int64(i.sB())
			var cond bool
			switch v := ra.(type) {
			case int64:
				cond = v == imm
			case float64:
				cond = v == float64(imm)
			}
			doCondJump(ci, cond, i.k() != 0)

		case opLessThanI:
			ra := frame[i.a()]
			imm := i.sB()
			var cond bool
			switch v := ra.(type) {
			case int64:
				cond = v < int64(imm)
			case float64:
				cond = v < float64(imm)
			default:
				cond = l.callOrderImmediate(ra, imm, false, i.c() != 0, tmLT)
				frame = ci.frame
			}
			doCondJump(ci, cond, i.k() != 0)

		case opLessOrEqualI:
			ra := frame[i.a()]
			imm := i.sB()
			var cond bool
			switch v := ra.(type) {
			case int64:
				cond = v <= int64(imm)
			case float64:
				cond = v <= float64(imm)
			default:
				cond = l.callOrderImmediate(ra, imm, false, i.c() != 0, tmLE)
				frame = ci.frame
			}
			doCondJump(ci, cond, i.k() != 0)

		case opGreaterThanI:
			ra := frame[i.a()]
			imm := i.sB()
			var cond bool
			switch v := ra.(type) {
			case int64:
				cond = v > int64(imm)
			case float64:
				cond = v > float64(imm)
			default:
				cond = l.callOrderImmediate(ra, imm, true, i.c() != 0, tmLT)
				frame = ci.frame
			}
			doCondJump(ci, cond, i.k() != 0)

		case opGreaterOrEqualI:
			ra := frame[i.a()]
			imm := i.sB()
			var cond bool
			switch v := ra.(type) {
			case int64:
				cond = v >= int64(imm)
			case float64:
				cond = v >= float64(imm)
			default:
				cond = l.callOrderImmediate(ra, imm, true, i.c() != 0, tmLE)
				frame = ci.frame
			}
			doCondJump(ci, cond, i.k() != 0)

		// --- Test / TestSet (5.4: k-bit for condition) ---
		case opTest:
			cond := !isFalse(frame[i.a()])
			doCondJump(ci, cond, i.k() != 0)

		case opTestSet:
			rb := frame[i.b()]
			cond := !isFalse(rb)
			if cond == (i.k() != 0) {
				frame[i.a()] = rb
				ji := ci.step()
				ci.jump(ji.sJ())
			} else {
				ci.skip()
			}

		// --- Call ---
		case opCall:
			a, b, c := i.a(), i.b(), i.c()
			if b != 0 {
				l.top = ci.stackIndex(a + b)
			}
			if n := c - 1; l.preCall(ci.stackIndex(a), n) {
				if n >= 0 {
					l.top = ci.top
				}
				frame = ci.frame
			} else {
				ci = l.callInfo
				ci.setCallStatus(callStatusReentry)
				frame, closure, constants = newFrame(l, ci)
			}

		case opTailCall:
			a, b := i.a(), i.b()
			if b != 0 {
				l.top = ci.stackIndex(a + b)
			}
			if i.k() != 0 {
				l.close(ci.base())
			}
			if l.preCall(ci.stackIndex(a), MultipleReturns) {
				frame = ci.frame
			} else {
				nci := l.callInfo
				oci := nci.previous
				nfn, ofn := nci.function, oci.function
				lim := nci.base() + l.stack[nfn].(*luaClosure).prototype.parameterCount
				if len(closure.prototype.prototypes) > 0 {
					l.close(oci.base())
				}
				for j := 0; nfn+j < lim; j++ {
					l.stack[ofn+j] = l.stack[nfn+j]
				}
				base := ofn + (nci.base() - nfn)
				oci.setTop(ofn + (l.top - nfn))
				oci.frame = l.stack[base:oci.top]
				oci.savedPC, oci.code = nci.savedPC, nci.code
				oci.setCallStatus(callStatusTail)
				l.top, l.callInfo, ci = oci.top, oci, oci
				frame, closure, constants = newFrame(l, ci)
			}

		case opReturn:
			a := i.a()
			b := i.b()
			if b != 0 {
				l.top = ci.stackIndex(a + b - 1)
			}
			if i.k() != 0 {
				ci.savedTop = l.top
				l.closeYieldable(ci.base())
			} else if len(closure.prototype.prototypes) > 0 {
				l.close(ci.base())
			}
			n := l.postCall(ci.stackIndex(a))
			if !ci.isCallStatus(callStatusReentry) {
				return
			}
			ci = l.callInfo
			if n {
				l.top = ci.top
			}
			frame, closure, constants = newFrame(l, ci)

		case opReturn0:
			if i.k() != 0 {
				l.closeYieldable(ci.base())
			} else if len(closure.prototype.prototypes) > 0 {
				l.close(ci.base())
			}
			l.top = ci.stackIndex(i.a())
			n := l.postCall(ci.stackIndex(i.a()))
			if !ci.isCallStatus(callStatusReentry) {
				return
			}
			ci = l.callInfo
			if n {
				l.top = ci.top
			}
			frame, closure, constants = newFrame(l, ci)

		case opReturn1:
			a := i.a()
			if i.k() != 0 {
				l.closeYieldable(ci.base())
			} else if len(closure.prototype.prototypes) > 0 {
				l.close(ci.base())
			}
			l.top = ci.stackIndex(a + 1)
			n := l.postCall(ci.stackIndex(a))
			if !ci.isCallStatus(callStatusReentry) {
				return
			}
			ci = l.callInfo
			if n {
				l.top = ci.top
			}
			frame, closure, constants = newFrame(l, ci)

		// --- For loops (5.4: Bx format, counter-based for integers) ---
		case opForLoop:
			a := i.a()
			if _, ok := frame[a+2].(int64); ok {
				// Integer loop: ra+1 is counter (unsigned)
				count := uint64(frame[a+1].(int64))
				if count > 0 {
					step := frame[a+2].(int64)
					idx := frame[a].(int64)
					frame[a+1] = int64(count - 1)
					idx = int64(uint64(idx) + uint64(step))
					frame[a] = idx
					frame[a+3] = idx
					ci.jump(-i.bx())
				}
			} else {
				// Float loop
				step := frame[a+2].(float64)
				limit := frame[a+1].(float64)
				idx := frame[a].(float64)
				idx += step
				if (step > 0 && idx <= limit) || (step <= 0 && limit <= idx) {
					frame[a] = idx
					frame[a+3] = idx
					ci.jump(-i.bx())
				}
			}

		case opForPrep:
			a := i.a()
			if iInit, initOk := frame[a].(int64); initOk {
				if iStep, stepOk := frame[a+2].(int64); stepOk {
					if iStep == 0 {
						l.runtimeError("'for' step is zero")
					}
					frame[a+3] = iInit // control variable
					iLimit, shouldSkip := l.forLimit54(frame[a+1], iInit, iStep)
					if shouldSkip {
						ci.jump(i.bx() + 1) // skip loop body + FORLOOP
						break
					}
					// Compute iteration counter
					var count uint64
					if iStep > 0 {
						count = uint64(iLimit) - uint64(iInit)
						if iStep != 1 {
							count /= uint64(iStep)
						}
					} else {
						count = uint64(iInit) - uint64(iLimit)
						count /= uint64(-(iStep+1)) + 1
					}
					frame[a+1] = int64(count) // store counter in place of limit
					// ra stays as init (unchanged)
					break
				}
			}
			// Float loop
			init, ok1 := l.toNumber(frame[a])
			limit, ok2 := l.toNumber(frame[a+1])
			step, ok3 := l.toNumber(frame[a+2])
			if !ok2 {
				l.runtimeError(fmt.Sprintf("bad 'for' limit (number expected, got %s)", l.valueTypeName(frame[a+1])))
			}
			if !ok3 {
				l.runtimeError(fmt.Sprintf("bad 'for' step (number expected, got %s)", l.valueTypeName(frame[a+2])))
			}
			if !ok1 {
				l.runtimeError(fmt.Sprintf("bad 'for' initial value (number expected, got %s)", l.valueTypeName(frame[a])))
			}
			if step == 0 {
				l.runtimeError("'for' step is zero")
			}
			if (step > 0 && limit < init) || (step <= 0 && init < limit) {
				ci.jump(i.bx() + 1) // skip loop
				break
			}
			frame[a] = init
			frame[a+1] = limit
			frame[a+2] = step
			frame[a+3] = init // control variable

		case opTForPrep:
			// Lua 5.4: mark R[A+3] as to-be-closed variable
			a := i.a()
			tbcIdx := ci.stackIndex(a + 3)
			v := l.stack[tbcIdx]
			if v != nil && v != false {
				if l.tagMethodByObject(v, tmClose) == nil {
					l.runtimeError("variable is not closable")
				}
				l.newTBCUpValue(tbcIdx)
			}
			// Jump forward to TFORCALL/TFORLOOP
			ci.jump(i.bx())

		case opTForCall:
			a := i.a()
			callBase := a + 4 // 5.4: results start at ra+4 (ra+3 is to-be-closed)
			copy(frame[callBase:callBase+3], frame[a:a+3])
			callBase += ci.base()
			l.top = callBase + 3
			l.call(callBase, i.c(), true)
			frame, l.top = ci.frame, ci.top
			i = expectNext(ci, opTForLoop)
			fallthrough

		case opTForLoop:
			// A = base+2 (control variable); first user var at A+2 = base+4
			a := i.a()
			if frame[a+2] != nil { // first user variable at ra+2
				frame[a] = frame[a+2] // update control variable
				ci.jump(-i.bx())      // jump back
			}

		case opSetList:
			a, n := i.a(), i.b()
			c := i.c()
			if n == 0 {
				n = l.top - ci.stackIndex(a) - 1
			} else {
				l.top = ci.top
			}
			if i.k() != 0 {
				c += expectNext(ci, opExtraArg).ax() * (maxArgC + 1)
			}
			h := frame[a].(*table)
			last := c + n
			if last > len(h.array) {
				h.extendArray(last)
			}
			copy(h.array[c:last], frame[a+1:a+1+n])
			l.top = ci.top

		case opClosure:
			a, p := i.a(), &closure.prototype.prototypes[i.bx()]
			if ncl := cached(p, closure.upValues, ci.base()); ncl == nil {
				frame[a] = l.newClosure(p, closure.upValues, ci.base())
			} else {
				frame[a] = ncl
			}
			clear(frame[a+1:])

		case opVarArg:
			a := i.a()
			b := i.c() - 1 // 5.4 uses C field, not B
			n := ci.base() - ci.function - closure.prototype.parameterCount - 1
			if b < 0 {
				b = n
				l.checkStack(n)
				l.top = ci.base() + a + n
				if ci.top < l.top {
					ci.setTop(l.top)
					ci.frame = l.stack[ci.base():ci.top]
				}
				frame = ci.frame
			}
			for j := 0; j < b; j++ {
				if j < n {
					frame[a+j] = l.stack[ci.base()-n+j]
				} else {
					frame[a+j] = nil
				}
			}

		case opVarArgPrep:
			// In Go, adjustVarArgs is already called in preCall.
			// Handle hook setup for vararg functions (matches C Lua OP_VARARGPREP).
			if l.hookMask != 0 {
				if l.hookMask&MaskCall != 0 {
					l.callHook(ci)
				}
				l.oldPC = 1 // next opcode will be seen as a "new" line
			}

		case opExtraArg:
			panic(fmt.Sprintf("unexpected opExtraArg instruction, '%s'", i.String()))
		}
	}
}

// doCondJump implements the 5.4 comparison jump pattern.
// If cond matches expected (k-bit), execute the next instruction as JMP.
// Otherwise, skip the next instruction (JMP).
func doCondJump(ci *callInfo, cond bool, expected bool) {
	if cond == expected {
		ji := ci.step()
		ci.jump(ji.sJ())
	} else {
		ci.skip()
	}
}
