package lua

import (
	"fmt"
	"sort"
)

type sortHelper struct {
	l           *State
	n           int
	hasFunction bool
}

func (h sortHelper) Len() int { return h.n }

func (h sortHelper) Swap(i, j int) {
	// Convert Go to Lua indices
	i++
	j++
	// Get t[i] and t[j] via __index
	h.l.PushInteger(i)
	h.l.Table(1) // t[i]
	h.l.PushInteger(j)
	h.l.Table(1) // t[j]
	// Set t[i] = old t[j] via __newindex
	h.l.PushInteger(i)
	h.l.Insert(-2) // key before value
	h.l.SetTable(1)
	// Set t[j] = old t[i] via __newindex
	h.l.PushInteger(j)
	h.l.Insert(-2) // key before value
	h.l.SetTable(1)
}

func (h sortHelper) Less(i, j int) bool {
	// Convert Go to Lua indices
	i++
	j++
	if h.hasFunction {
		h.l.PushValue(2)
		// Get t[i] and t[j] via __index
		h.l.PushInteger(i)
		h.l.Table(1)
		h.l.PushInteger(j)
		h.l.Table(1)
		h.l.Call(2, 1)
		b := h.l.ToBoolean(-1)
		h.l.Pop(1)
		return b
	}
	// Get t[i] and t[j] via __index
	h.l.PushInteger(i)
	h.l.Table(1)
	h.l.PushInteger(j)
	h.l.Table(1)
	b := h.l.Compare(-2, -1, OpLT)
	h.l.Pop(2)
	return b
}

var tableLibrary = []RegistryFunction{
	{"concat", func(l *State) int {
		CheckType(l, 1, TypeTable)
		sep := OptString(l, 2, "")
		i := OptInteger(l, 3, 1)
		var last int
		if l.IsNoneOrNil(4) {
			last = LengthEx(l, 1)
		} else {
			last = CheckInteger(l, 4)
		}
		s := ""
		addField := func() {
			// Get t[i] via __index
			l.PushInteger(i)
			l.Table(1)
			if str, ok := l.ToString(-1); ok {
				s += str
			} else {
				Errorf(l, fmt.Sprintf("invalid value (%s) at index %d in table for 'concat'", TypeNameOf(l, -1), i))
			}
			l.Pop(1)
		}
		for ; i < last; i++ {
			addField()
			s += sep
		}
		if i == last {
			addField()
		}
		l.PushString(s)
		return 1
	}},
	{"insert", func(l *State) int {
		CheckType(l, 1, TypeTable)
		e := LengthEx(l, 1) + 1 // First empty element.
		switch l.Top() {
		case 2:
			// Insert new element at the end (value is at top)
			l.PushInteger(e)
			l.Insert(-2) // key before value
			l.SetTable(1)
		case 3:
			pos := CheckInteger(l, 2)
			ArgumentCheck(l, 1 <= pos && pos <= e, 2, "position out of bounds")
			for i := e; i > pos; i-- {
				// t[i] = t[i-1]
				l.PushInteger(i - 1)
				l.Table(1) // get t[i-1]
				l.PushInteger(i)
				l.Insert(-2)  // key before value
				l.SetTable(1) // set t[i]
			}
			// t[pos] = v (value was at index 3)
			l.PushInteger(pos)
			l.Insert(-2)  // key before value
			l.SetTable(1) // set t[pos]
		default:
			Errorf(l, "wrong number of arguments to 'insert'")
		}
		return 0
	}},
	{"pack", func(l *State) int {
		n := l.Top()
		l.CreateTable(n, 1)
		l.PushInteger(n)
		l.SetField(-2, "n")
		if n > 0 {
			l.PushValue(1)
			l.RawSetInt(-2, 1)
			l.Replace(1)
			for i := n; i >= 2; i-- {
				l.RawSetInt(1, i)
			}
		}
		return 1
	}},
	{"unpack", func(l *State) int {
		CheckType(l, 1, TypeTable)
		i := OptInteger(l, 2, 1)
		var e int
		if l.IsNoneOrNil(3) {
			e = LengthEx(l, 1)
		} else {
			e = CheckInteger(l, 3)
		}
		if i > e {
			return 0
		}
		n := e - i + 1
		if n <= 0 || !l.CheckStack(n) {
			Errorf(l, "too many results to unpack")
			panic("unreachable")
		}
		// Get all elements via __index
		// Use countdown to avoid integer overflow when i == maxInt
		for j := 0; j < n; j++ {
			l.PushInteger(i + j)
			l.Table(1) // get t[i+j]
		}
		return n
	}},
	{"remove", func(l *State) int {
		CheckType(l, 1, TypeTable)
		size := LengthEx(l, 1)
		pos := OptInteger(l, 2, size)
		if pos != size {
			ArgumentCheck(l, 1 <= pos && pos <= size+1, 2, "position out of bounds")
		}
		// Get element to return: push key, get value via __index
		l.PushInteger(pos)
		l.Table(1) // get t[pos], push to stack (this is our return value)
		for ; pos < size; pos++ {
			// t[pos] = t[pos+1]
			l.PushInteger(pos + 1)
			l.Table(1) // get t[pos+1]
			l.PushInteger(pos)
			l.Insert(-2)  // key before value
			l.SetTable(1) // set t[pos]
		}
		// t[pos] = nil
		l.PushInteger(pos)
		l.PushNil()
		l.SetTable(1)
		return 1
	}},
	{"sort", func(l *State) int {
		CheckType(l, 1, TypeTable)
		n := LengthEx(l, 1)
		// Lua 5.3: array too big check (n < INT_MAX, where INT_MAX is typically 2^31-1)
		ArgumentCheck(l, n < (1<<31-1), 1, "array too big")
		hasFunction := !l.IsNoneOrNil(2)
		if hasFunction {
			CheckType(l, 2, TypeFunction)
		}
		l.SetTop(2)
		// Ensure stack space for sort operations. Swap/Less use up to 5 slots
		// directly, plus metamethods (__index/__newindex) may use more.
		l.CheckStack(40)
		h := sortHelper{l, n, hasFunction}
		sort.Sort(h)
		// Check result is sorted.
		if n > 0 && h.Less(n-1, 0) {
			Errorf(l, "invalid order function for sorting")
		}
		return 0
	}},
	// Lua 5.3: table.move
	{"move", func(l *State) int {
		CheckType(l, 1, TypeTable)
		f := CheckInteger(l, 2)
		e := CheckInteger(l, 3)
		t := CheckInteger(l, 4)
		var tt int // destination table stack index
		if !l.IsNoneOrNil(5) {
			CheckType(l, 5, TypeTable)
			tt = 5
		} else {
			tt = 1 // default: same table
		}
		// Check for valid range
		if e >= f {
			// Check for "too many elements to move" (Lua 5.3: f > 0 || e < LUA_MAXINTEGER + f)
			ArgumentCheck(l, f > 0 || e < maxInt+f, 3, "too many elements to move")
			n := e - f + 1 // number of elements to move
			ArgumentCheck(l, t <= maxInt-n+1, 4, "destination wrap around")
			// Check if tables are the same (not just stack index, but actual identity)
			sameTable := l.RawEqual(1, tt)
			// Helper to get value respecting __index
			getVal := func(idx int) {
				l.PushInteger(idx)
				l.Table(1) // pops key, pushes value
			}
			// Helper to set value respecting __newindex
			setVal := func(idx int) {
				l.PushInteger(idx)
				l.Insert(-2)   // key before value
				l.SetTable(tt) // pops key and value
			}
			if t > e || t <= f || !sameTable {
				// Non-overlapping or different tables: copy forward
				for i := 0; i < n; i++ {
					getVal(f + i)
					setVal(t + i)
				}
			} else {
				// Overlapping, destination after source in same table: copy backward
				for i := n - 1; i >= 0; i-- {
					getVal(f + i)
					setVal(t + i)
				}
			}
		}
		l.PushValue(tt)
		return 1
	}},
}

// TableOpen opens the table library. Usually passed to Require.
func TableOpen(l *State) int {
	NewLibrary(l, tableLibrary)
	return 1
}
