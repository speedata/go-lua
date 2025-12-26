package lua

type tm uint

const (
	tmIndex tm = iota
	tmNewIndex
	tmGC
	tmMode
	tmLen
	tmEq
	tmAdd
	tmSub
	tmMul
	tmMod // Lua 5.3: MOD before POW
	tmPow
	tmDiv
	tmIDiv // Lua 5.3: Integer division
	tmBAnd // Lua 5.3: Bitwise AND
	tmBOr  // Lua 5.3: Bitwise OR
	tmBXor // Lua 5.3: Bitwise XOR
	tmShl  // Lua 5.3: Shift left
	tmShr  // Lua 5.3: Shift right
	tmUnaryMinus
	tmBNot // Lua 5.3: Bitwise NOT
	tmLT
	tmLE
	tmConcat
	tmCall
	tmCount // number of tag methods
)

var eventNames = []string{
	"__index",
	"__newindex",
	"__gc",
	"__mode",
	"__len",
	"__eq",
	"__add",
	"__sub",
	"__mul",
	"__mod",
	"__pow",
	"__div",
	"__idiv",
	"__band",
	"__bor",
	"__bxor",
	"__shl",
	"__shr",
	"__unm",
	"__bnot",
	"__lt",
	"__le",
	"__concat",
	"__call",
}

var typeNames = []string{
	"no value",
	"nil",
	"boolean",
	"userdata",
	"number",
	"string",
	"table",
	"function",
	"userdata",
	"thread",
	"proto", // these last two cases are used for tests only
	"upval",
}

func (events *table) tagMethod(event tm, name string) value {
	tm := events.atString(name)
	//l.assert(event <= tmEq)
	if tm == nil {
		events.flags |= 1 << event
	}
	return tm
}

func (l *State) tagMethodByObject(o value, event tm) value {
	var mt *table
	switch o := o.(type) {
	case *table:
		mt = o.metaTable
	case *userData:
		mt = o.metaTable
	default:
		mt = l.global.metaTable(o)
	}
	if mt == nil {
		return nil
	}
	return mt.atString(l.global.tagMethodNames[event])
}

func (l *State) callTagMethod(f, p1, p2 value) value {
	l.push(f)
	l.push(p1)
	l.push(p2)
	l.call(l.top-3, 1, l.callInfo.isLua())
	return l.pop()
}

func (l *State) callTagMethodV(f, p1, p2, p3 value) {
	l.push(f)
	l.push(p1)
	l.push(p2)
	l.push(p3)
	l.call(l.top-4, 0, l.callInfo.isLua())
}
