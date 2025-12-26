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

func reduce(f func(float64, float64) float64) Function {
	return func(l *State) int {
		n := l.Top() // number of arguments
		v := CheckNumber(l, 1)
		for i := 2; i <= n; i++ {
			v = f(v, CheckNumber(l, i))
		}
		l.PushNumber(v)
		return 1
	}
}

var mathLibrary = []RegistryFunction{
	{"abs", mathUnaryOp(math.Abs)},
	{"acos", mathUnaryOp(math.Acos)},
	{"asin", mathUnaryOp(math.Asin)},
	{"atan2", mathBinaryOp(math.Atan2)},
	{"atan", mathUnaryOp(math.Atan)},
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
	{"fmod", mathBinaryOp(math.Mod)},
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
	{"max", reduce(math.Max)},
	{"min", reduce(math.Min)},
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
		r := rand.Float64()
		switch l.Top() {
		case 0: // no arguments
			l.PushNumber(r)
		case 1: // upper limit only
			u := CheckNumber(l, 1)
			ArgumentCheck(l, 1.0 <= u, 1, "interval is empty")
			l.PushNumber(math.Floor(r*u) + 1.0) // [1, u]
		case 2: // lower and upper limits
			lo, u := CheckNumber(l, 1), CheckNumber(l, 2)
			ArgumentCheck(l, lo <= u, 2, "interval is empty")
			l.PushNumber(math.Floor(r*(u-lo+1)) + lo) // [lo, u]
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
			if i := int64(v); float64(i) == v {
				l.PushInteger64(i)
			} else {
				l.PushNil()
			}
		default:
			// Try string conversion
			if n, ok := l.ToNumber(1); ok {
				if i := int64(n); float64(i) == n {
					l.PushInteger64(i)
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
