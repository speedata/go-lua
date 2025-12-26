package lua

import (
	"math"
	"math/rand"
)

const radiansPerDegree = math.Pi / 180.0

func mathUnaryOp(f func(float64) float64) Function {
	return func(l *State) int {
		l.PushNumber(f(CheckNumber(l, 1)))
		return 1
	}
}

func mathBinaryOp(f func(float64, float64) float64) Function {
	return func(l *State) int {
		l.PushNumber(f(CheckNumber(l, 1), CheckNumber(l, 2)))
		return 1
	}
}

// reduce creates a min/max function that preserves integer type in Lua 5.3
func reduce(f func(float64, float64) float64, isMax bool) Function {
	return func(l *State) int {
		n := l.Top()   // number of arguments
		CheckAny(l, 1) // "value expected" error if no arguments

		// Track if all arguments are integers and result should be integer
		allInt := true
		var intResult int64
		var floatResult float64

		for i := 1; i <= n; i++ {
			if allInt && l.IsInteger(i) {
				v, _ := l.ToInteger64(i)
				if i == 1 {
					intResult = v
				} else {
					if isMax {
						if v > intResult {
							intResult = v
						}
					} else {
						if v < intResult {
							intResult = v
						}
					}
				}
			} else {
				// Switch to float mode
				if allInt {
					floatResult = float64(intResult)
					allInt = false
				}
				v := CheckNumber(l, i)
				if i == 1 || allInt {
					floatResult = v
				} else {
					floatResult = f(floatResult, v)
				}
			}
		}

		if allInt {
			l.PushInteger64(intResult)
		} else {
			l.PushNumber(floatResult)
		}
		return 1
	}
}

var mathLibrary = []RegistryFunction{
	{"abs", func(l *State) int {
		// Lua 5.3: abs preserves integer type
		if l.IsInteger(1) {
			i, _ := l.ToInteger64(1)
			if i < 0 {
				i = -i // overflow wraps for minint
			}
			l.PushInteger64(i)
		} else {
			l.PushNumber(math.Abs(CheckNumber(l, 1)))
		}
		return 1
	}},
	{"acos", mathUnaryOp(math.Acos)},
	{"asin", mathUnaryOp(math.Asin)},
	{"atan2", mathBinaryOp(math.Atan2)},
	{"atan", func(l *State) int {
		// Lua 5.3: atan(y [, x]) - if x is given, returns atan2(y, x)
		y := CheckNumber(l, 1)
		if l.IsNoneOrNil(2) {
			l.PushNumber(math.Atan(y))
		} else {
			x := CheckNumber(l, 2)
			l.PushNumber(math.Atan2(y, x))
		}
		return 1
	}},
	{"ceil", func(l *State) int {
		// Lua 5.3: ceil returns integer when result fits
		x := CheckNumber(l, 1)
		c := math.Ceil(x)
		if i := int64(c); float64(i) == c && c >= float64(math.MinInt64) && c <= float64(math.MaxInt64) {
			l.PushInteger64(i)
		} else {
			l.PushNumber(c)
		}
		return 1
	}},
	{"cosh", mathUnaryOp(math.Cosh)},
	{"cos", mathUnaryOp(math.Cos)},
	{"deg", mathUnaryOp(func(x float64) float64 { return x / radiansPerDegree })},
	{"exp", mathUnaryOp(math.Exp)},
	{"floor", func(l *State) int {
		// Lua 5.3: floor returns integer when result fits
		x := CheckNumber(l, 1)
		f := math.Floor(x)
		if i := int64(f); float64(i) == f && f >= float64(math.MinInt64) && f <= float64(math.MaxInt64) {
			l.PushInteger64(i)
		} else {
			l.PushNumber(f)
		}
		return 1
	}},
	{"fmod", func(l *State) int {
		// Lua 5.3: fmod preserves integer type when both args are integers
		if l.IsInteger(1) && l.IsInteger(2) {
			x, _ := l.ToInteger64(1)
			y, _ := l.ToInteger64(2)
			if y == 0 {
				Errorf(l, "zero")
			}
			l.PushInteger64(x % y)
		} else {
			l.PushNumber(math.Mod(CheckNumber(l, 1), CheckNumber(l, 2)))
		}
		return 1
	}},
	{"frexp", func(l *State) int {
		f, e := math.Frexp(CheckNumber(l, 1))
		l.PushNumber(f)
		l.PushInteger(e)
		return 2
	}},
	{"ldexp", func(l *State) int {
		x, e := CheckNumber(l, 1), CheckInteger(l, 2)
		l.PushNumber(math.Ldexp(x, e))
		return 1
	}},
	{"log", func(l *State) int {
		x := CheckNumber(l, 1)
		if l.IsNoneOrNil(2) {
			l.PushNumber(math.Log(x))
		} else if base := CheckNumber(l, 2); base == 10.0 {
			l.PushNumber(math.Log10(x))
		} else {
			l.PushNumber(math.Log(x) / math.Log(base))
		}
		return 1
	}},
	{"max", reduce(math.Max, true)},
	{"min", reduce(math.Min, false)},
	{"modf", func(l *State) int {
		// Lua 5.3: first return value is integer when it fits
		n := CheckNumber(l, 1)
		// Handle infinity: Lua returns (±inf, 0.0), Go returns (±inf, NaN)
		if math.IsInf(n, 0) {
			l.PushNumber(n)
			l.PushNumber(0.0)
			return 2
		}
		i, f := math.Modf(n)
		if ii := int64(i); float64(ii) == i && i >= float64(math.MinInt64) && i <= float64(math.MaxInt64) {
			l.PushInteger64(ii)
		} else {
			l.PushNumber(i)
		}
		l.PushNumber(f)
		return 2
	}},
	{"pow", mathBinaryOp(math.Pow)},
	{"rad", mathUnaryOp(func(x float64) float64 { return x * radiansPerDegree })},
	{"random", func(l *State) int {
		// Helper to get int64 argument
		checkInt64 := func(index int) int64 {
			i, ok := l.ToInteger64(index)
			if !ok {
				ArgumentError(l, index, "integer expected")
			}
			return i
		}
		// randRange returns a random int64 in [lo, u] inclusive
		// Returns (result, ok) where ok is false if range is too large
		randRange := func(lo, u int64) (int64, bool) {
			if lo == u {
				return lo, true
			}
			// Use uint64 arithmetic to avoid overflow
			rangeLow := uint64(lo - math.MinInt64) // shift to [0, 2^64 - 1] range
			rangeHigh := uint64(u - math.MinInt64)
			rangeSize := rangeHigh - rangeLow + 1
			if rangeSize == 0 {
				// Would need full 64-bit range - this is too large
				return 0, false
			}
			// Lua 5.3 allows ranges up to 2^63 (half the 64-bit space)
			// Ranges larger than this are rejected as "too large"
			const maxRange = uint64(1) << 63
			if rangeSize > maxRange {
				return 0, false
			}
			// Random in [0, rangeSize), then shift back
			r := rand.Uint64() % rangeSize
			return int64(r+rangeLow) + math.MinInt64, true
		}
		switch l.Top() {
		case 0: // no arguments - returns float in [0,1)
			l.PushNumber(rand.Float64())
		case 1: // upper limit only - returns integer in [1, u]
			u := checkInt64(1)
			ArgumentCheck(l, 1 <= u, 1, "interval is empty")
			r, ok := randRange(1, u)
			if !ok {
				Errorf(l, "interval too large")
			}
			l.PushInteger64(r)
		case 2: // lower and upper limits - returns integer in [lo, u]
			lo := checkInt64(1)
			u := checkInt64(2)
			ArgumentCheck(l, lo <= u, 2, "interval is empty")
			r, ok := randRange(lo, u)
			if !ok {
				Errorf(l, "interval too large")
			}
			l.PushInteger64(r)
		default:
			Errorf(l, "wrong number of arguments")
		}
		return 1
	}},
	{"randomseed", func(l *State) int {
		rand.Seed(int64(CheckUnsigned(l, 1)))
		rand.Float64() // discard first value to avoid undesirable correlations
		return 0
	}},
	{"sinh", mathUnaryOp(math.Sinh)},
	{"sin", mathUnaryOp(math.Sin)},
	{"sqrt", mathUnaryOp(math.Sqrt)},
	{"tanh", mathUnaryOp(math.Tanh)},
	{"tan", mathUnaryOp(math.Tan)},
	// Lua 5.3: integer functions
	{"tointeger", func(l *State) int {
		switch v := l.ToValue(1).(type) {
		case int64:
			l.PushInteger64(v)
		case float64:
			// Check range before conversion to avoid overflow
			// float64 can represent values outside int64 range
			const maxInt64Float = float64(1 << 63) // 2^63
			if v >= maxInt64Float || v < -maxInt64Float {
				l.PushNil()
			} else if i := int64(v); float64(i) == v {
				l.PushInteger64(i)
			} else {
				l.PushNil()
			}
		default:
			// Try string conversion - use parseNumberEx to preserve integer precision
			if s, ok := l.ToValue(1).(string); ok {
				if intVal, floatVal, isInt, ok := l.parseNumberEx(s); ok {
					if isInt {
						l.PushInteger64(intVal)
					} else {
						// Float value - apply same range check
						const maxInt64Float = float64(1 << 63)
						if floatVal >= maxInt64Float || floatVal < -maxInt64Float {
							l.PushNil()
						} else if i := int64(floatVal); float64(i) == floatVal {
							l.PushInteger64(i)
						} else {
							l.PushNil()
						}
					}
				} else {
					l.PushNil()
				}
			} else {
				l.PushNil()
			}
		}
		return 1
	}},
	{"type", func(l *State) int {
		// Check actual type, not convertible type (strings should return nil)
		v := l.ToValue(1)
		switch v.(type) {
		case int64:
			l.PushString("integer")
		case float64:
			l.PushString("float")
		default:
			l.PushNil()
		}
		return 1
	}},
	{"ult", func(l *State) int {
		a, ok1 := l.ToInteger64(1)
		b, ok2 := l.ToInteger64(2)
		if !ok1 {
			ArgumentError(l, 1, "number has no integer representation")
		}
		if !ok2 {
			ArgumentError(l, 2, "number has no integer representation")
		}
		l.PushBoolean(uint64(a) < uint64(b))
		return 1
	}},
}

// MathOpen opens the math library. Usually passed to Require.
func MathOpen(l *State) int {
	NewLibrary(l, mathLibrary)
	l.PushNumber(3.1415926535897932384626433832795) // TODO use math.Pi instead? Values differ.
	l.SetField(-2, "pi")
	l.PushNumber(math.Inf(1)) // Lua defines math.huge as infinity
	l.SetField(-2, "huge")
	// Lua 5.3: integer limits
	l.PushInteger(math.MaxInt64)
	l.SetField(-2, "maxinteger")
	l.PushInteger(math.MinInt64)
	l.SetField(-2, "mininteger")
	return 1
}
