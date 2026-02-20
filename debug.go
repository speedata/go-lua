package lua

import (
	"fmt"
	"strings"
)

// A Frame is a token representing an activation record. It is returned by
// Stack and passed to Info.
type Frame *callInfo

func (l *State) resetHookCount() { l.hookCount = l.baseHookCount }
func (l *State) prototype(ci *callInfo) *prototype {
	return l.stack[ci.function].(*luaClosure).prototype
}
func (l *State) currentLine(ci *callInfo) int {
	return getFuncLine(l.prototype(ci), int(ci.savedPC-1))
}

func chunkID(source string) string {
	if len(source) == 0 {
		return "[string \"\"]"
	}
	bufflen := idSize // available characters (including '\0' in C, we use as max length)
	switch source[0] {
	case '=': // "literal" source
		if len(source)-1 <= bufflen-1 {
			return source[1:]
		}
		return source[1:bufflen]
	case '@': // file name
		if len(source)-1 <= bufflen-1 {
			return source[1:]
		}
		// truncate beginning, keep end with "..." prefix
		rest := bufflen - 1 - 3 // -1 for removing '@', -3 for "..."
		return "..." + source[len(source)-rest:]
	}
	// string source: format as [string "source"]
	nl := strings.IndexByte(source, '\n')
	pre := "[string \""
	suf := "\"]"
	dots := "..."
	avail := bufflen - len(pre) - len(dots) - len(suf) - 1
	l := len(source)
	if l <= avail+len(dots) && nl < 0 { // small one-line source?
		return pre + source + suf
	}
	if nl >= 0 && nl < l {
		l = nl
	}
	if l > avail {
		l = avail
	}
	return pre + source[:l] + dots + suf
}

func (l *State) runtimeError(message string) {
	l.push(message)
	if ci := l.callInfo; ci.isLua() {
		line, source := l.currentLine(ci), l.prototype(ci).source
		if source == "" {
			source = "?"
		} else {
			source = chunkID(source)
		}
		l.push(fmt.Sprintf("%s:%d: %s", source, line, message))
	}
	l.errorMessage()
}

// varInfo finds the variable name and kind for a value in the current Lua frame.
// Like C Lua's varinfo(), it uses symbolic execution of the bytecode to identify
// the source of a value. When stackIdx >= 0, it uses exact stack position matching;
// otherwise it falls back to value comparison for finding the frame slot.
func (l *State) varInfo(v value, stackIdx int) (kind, name string) {
	ci := l.callInfo
	if !ci.isLua() {
		return
	}
	c := l.stack[ci.function].(*luaClosure)
	currentPC := ci.savedPC - 1

	// Check upvalues by stack identity (like C Lua's getupvalname).
	// Only works when we know the exact stack slot, because Go interface
	// comparison can match the wrong upvalue when multiple have the same value.
	if stackIdx >= 0 {
		for i, uv := range c.upValues {
			if home, ok := uv.home.(stackLocation); ok {
				if home.index == stackIdx {
					return "upvalue", c.prototype.upValueName(i)
				}
			}
		}
	}

	// Find register index in frame
	frameIndex := -1
	if stackIdx >= 0 {
		base := ci.base()
		fi := stackIdx - base
		if fi >= 0 && fi < len(ci.frame) {
			frameIndex = fi
		}
	} else {
		for i, e := range ci.frame {
			if e == v {
				frameIndex = i
				break
			}
		}
	}
	if frameIndex >= 0 {
		name, kind = c.prototype.objectName(frameIndex, currentPC)
	}

	// If objectName didn't find anything, check the current instruction
	// for direct upvalue access (GETTABUP/SETTABUP/GETUPVAL).
	// This handles the case where the value came directly from an upvalue
	// and was never stored in a register (Go can't do pointer identity like C).
	if kind == "" && int(currentPC) < len(c.prototype.code) {
		instr := c.prototype.code[currentPC]
		switch instr.opCode() {
		case opGetTableUp:
			// GETTABUP A B C: table is upvalue at B
			return "upvalue", c.prototype.upValueName(instr.b())
		case opSetTableUp:
			// SETTABUP A B C: table is upvalue at A
			return "upvalue", c.prototype.upValueName(instr.a())
		}
	}
	return
}

// objectTypeName returns the type name for a value, checking __name metafield first.
func (l *State) objectTypeName(v value) string {
	var mt *table
	switch v := v.(type) {
	case *table:
		mt = v.metaTable
	case *userData:
		mt = v.metaTable
	}
	if mt != nil {
		if name, ok := mt.atString("__name").(string); ok {
			return name
		}
	}
	return l.valueToType(v).String()
}

func (l *State) typeError(v value, operation string) {
	typeName := l.objectTypeName(v)
	if kind, name := l.varInfo(v, -1); kind != "" {
		l.runtimeError(fmt.Sprintf("attempt to %s a %s value (%s '%s')", operation, typeName, kind, name))
	}
	l.runtimeError(fmt.Sprintf("attempt to %s a %s value", operation, typeName))
}

func (l *State) typeErrorAt(stackIdx int, operation string) {
	v := l.stack[stackIdx]
	typeName := l.objectTypeName(v)
	if kind, name := l.varInfo(v, stackIdx); kind != "" {
		l.runtimeError(fmt.Sprintf("attempt to %s a %s value (%s '%s')", operation, typeName, kind, name))
	}
	// For "call" operations, check the calling instruction context as fallback.
	// This handles __close calls where the value was pushed by Go code (not bytecode),
	// so varInfo can't find it. Like C Lua's funcnamefromcall in luaG_callerror.
	if operation == "call" {
		if ci := l.callInfo; ci.isLua() {
			name, kind := l.functionName(ci)
			if kind != "" {
				l.runtimeError(fmt.Sprintf("attempt to %s a %s value (%s '%s')", operation, typeName, kind, name))
			}
		}
	}
	l.runtimeError(fmt.Sprintf("attempt to %s a %s value", operation, typeName))
}

func (l *State) orderError(left, right value) {
	leftType, rightType := l.objectTypeName(left), l.objectTypeName(right)
	if leftType == rightType {
		l.runtimeError(fmt.Sprintf("attempt to compare two %s values", leftType))
	}
	l.runtimeError(fmt.Sprintf("attempt to compare %s with %s", leftType, rightType))
}

func (l *State) arithError(v1, v2 value) {
	if _, ok := l.toNumber(v1); !ok {
		v2 = v1
	}
	l.typeError(v2, "perform arithmetic on")
}

// bitwiseError reports an error for bitwise operations.
// If either operand is a float that can't be converted to an integer,
// it reports "number has no integer representation". Otherwise, it
// falls back to standard arithmetic error.
func (l *State) bitwiseError(v1, v2 value) {
	// Helper to check if a float can't be converted to integer
	cantConvert := func(f float64) bool {
		const pow2_63 = float64(1 << 63)
		if f >= pow2_63 || f < -pow2_63 {
			return true
		}
		return float64(int64(f)) != f
	}

	// Helper to get operand name from debug info
	getOperandName := func(v value) string {
		ci := l.callInfo
		if !ci.isLua() {
			return ""
		}
		c := l.stack[ci.function].(*luaClosure)
		// Check upvalues first
		for i, uv := range c.upValues {
			if uv.value() == v {
				return fmt.Sprintf("upvalue '%s'", c.prototype.upValueName(i))
			}
		}
		// Check stack frame
		for i, e := range ci.frame {
			if e == v {
				name, kind := c.prototype.objectName(i, ci.savedPC-1)
				if kind != "" {
					return fmt.Sprintf("%s '%s'", kind, name)
				}
				break
			}
		}
		return ""
	}

	// Check if v1 is a float that can't be converted to integer
	if f, ok := v1.(float64); ok && cantConvert(f) {
		if name := getOperandName(v1); name != "" {
			l.runtimeError(fmt.Sprintf("number (%s) has no integer representation", name))
		}
		l.runtimeError("number has no integer representation")
	}
	// Check if v2 is a float that can't be converted to integer
	if f, ok := v2.(float64); ok && cantConvert(f) {
		if name := getOperandName(v2); name != "" {
			l.runtimeError(fmt.Sprintf("number (%s) has no integer representation", name))
		}
		l.runtimeError("number has no integer representation")
	}
	// Otherwise, report bitwise operation error (for non-numeric types)
	if _, ok := l.toNumber(v1); !ok {
		v2 = v1
	}
	l.typeError(v2, "perform bitwise operation on")
}

func (l *State) concatError(v1, v2 value) {
	_, isString := v1.(string)
	_, isFloat := v1.(float64)
	_, isInt := v1.(int64)
	if isString || isFloat || isInt {
		v1 = v2
	}
	_, isString = v1.(string)
	_, isFloat = v1.(float64)
	_, isInt = v1.(int64)
	l.assert(!isString && !isFloat && !isInt)
	l.typeError(v1, "concatenate")
}

func (l *State) assert(cond bool) {
	if !cond {
		l.runtimeError("assertion failure")
	}
}

func (l *State) errorMessage() {
	if l.errorFunction != 0 { // is there an error handling function?
		errorFunction := l.stack[l.errorFunction]
		switch errorFunction.(type) {
		case closure:
		case *goFunction:
		default:
			l.throw(ErrorError)
		}
		l.stack[l.top] = l.stack[l.top-1] // move argument
		l.stack[l.top-1] = errorFunction  // push function
		l.top++
		savedEF := l.errorFunction
		l.errorFunction = 0 // prevent recursive error handler calls
		if err := l.protect(func() { l.call(l.top-2, 1, false) }); err != nil {
			_ = savedEF
			l.throw(ErrorError) // error in error handler
		}
	}
	// In Lua 5.3, error() can be called with any value, not just strings.
	// The actual error value stays on the stack and is used by setErrorObject.
	// We only use the string representation for RuntimeError if available.
	var msg string
	if s, ok := l.stack[l.top-1].(string); ok {
		msg = s
	}
	l.throw(RuntimeError(msg))
}

// SetDebugHook sets the debugging hook function.
//
// f is the hook function. mask specifies on which events the hook will be
// called: it is formed by a bitwise or of the constants MaskCall, MaskReturn,
// MaskLine, and MaskCount. The count argument is only meaningful when the
// mask includes MaskCount. For each event, the hook is called as explained
// below:
//
// Call hook is called when the interpreter calls a function. The hook is
// called just after Lua enters the new function, before the function gets
// its arguments.
//
// Return hook is called when the interpreter returns from a function. The
// hook is called just before Lua leaves the function. There is no standard
// way to access the values to be returned by the function.
//
// Line hook is called when the interpreter is about to start the execution
// of a new line of code, or when it jumps back in the code (even to the same
// line). (This event only happens while Lua is executing a Lua function.)
//
// Count hook is called after the interpreter executes every count
// instructions. (This event only happens while Lua is executing a Lua
// function.)
//
// A hook is disabled by setting mask to zero.
func SetDebugHook(l *State, f Hook, mask byte, count int) {
	if f == nil || mask == 0 {
		f, mask = nil, 0
	}
	if ci := l.callInfo; ci.isLua() {
		l.oldPC = ci.savedPC
	}
	l.hooker, l.baseHookCount = f, count
	l.resetHookCount()
	l.hookMask = mask
	l.internalHook = false
}

// DebugHook returns the current hook function.
func DebugHook(l *State) Hook { return l.hooker }

// DebugHookMask returns the current hook mask.
func DebugHookMask(l *State) byte { return l.hookMask }

// DebugHookCount returns the current hook count.
func DebugHookCount(l *State) int { return l.baseHookCount }

// Stack gets information about the interpreter runtime stack.
//
// It returns a Frame identifying the activation record of the
// function executing at a given level. Level 0 is the current running
// function, whereas level n+1 is the function that has called level n (except
// for tail calls, which do not count on the stack). When there are no errors,
// Stack returns true; when called with a level greater than the stack depth,
// it returns false.
func Stack(l *State, level int) (f Frame, ok bool) {
	if level < 0 {
		return // invalid (negative) level
	}
	callInfo := l.callInfo
	for ; level > 0 && callInfo != &l.baseCallInfo; level, callInfo = level-1, callInfo.previous {
	}
	if level == 0 && callInfo != &l.baseCallInfo { // level found?
		f, ok = callInfo, true
	}
	return
}

func functionInfo(p Debug, f closure) (d Debug) {
	d = p
	if l, ok := f.(*luaClosure); !ok {
		d.Source = "=[C]"
		d.LineDefined, d.LastLineDefined = -1, -1
		d.What = "C"
	} else {
		p := l.prototype
		d.Source = p.source
		d.LineDefined, d.LastLineDefined = p.lineDefined, p.lastLineDefined
		d.What = "Lua"
		if d.LineDefined == 0 {
			d.What = "main"
		}
	}
	d.ShortSource = chunkID(d.Source)
	return
}

func (l *State) functionName(ci *callInfo) (name, kind string) {
	if ci == &l.baseCallInfo {
		return
	}
	if ci.isCallStatus(callStatusHooked) {
		return "?", "hook"
	}
	var tm tm
	p := l.prototype(ci)
	// savedPC points to the NEXT instruction to execute, so subtract 1
	// to get the actual call instruction
	pc := ci.savedPC - 1
	if pc < 0 {
		return
	}
	switch i := p.code[pc]; i.opCode() {
	case opCall, opTailCall:
		return p.objectName(i.a(), pc)
	case opTForCall:
		return "for iterator", "for iterator"
	case opSelf, opGetTableUp, opGetTable, opGetI, opGetField:
		tm = tmIndex
	case opSetTableUp, opSetTable, opSetI, opSetField:
		tm = tmNewIndex
	case opMMBin, opMMBinI, opMMBinK:
		tm = tmFromC(i.c()) // C field holds the TM event
	case opEqual, opEqualI, opEqualK:
		tm = tmEq
	case opAdd:
		tm = tmAdd
	case opSub:
		tm = tmSub
	case opMul:
		tm = tmMul
	case opDiv:
		tm = tmDiv
	case opIDiv:
		tm = tmIDiv
	case opMod:
		tm = tmMod
	case opPow:
		tm = tmPow
	case opUnaryMinus:
		tm = tmUnaryMinus
	case opBNot:
		tm = tmBNot
	case opLength:
		tm = tmLen
	case opBAnd:
		tm = tmBAnd
	case opBOr:
		tm = tmBOr
	case opBXor:
		tm = tmBXor
	case opShl:
		tm = tmShl
	case opShr:
		tm = tmShr
	case opLessThan, opLessThanI, opGreaterThanI:
		tm = tmLT
	case opLessOrEqual, opLessOrEqualI, opGreaterOrEqualI:
		tm = tmLE
	case opConcat:
		tm = tmConcat
	case opClose, opReturn, opReturn0, opReturn1:
		tm = tmClose
	default:
		return
	}
	// Strip "__" prefix from event name (like C Lua's +2 offset)
	name = eventNames[tm]
	if len(name) > 2 && name[:2] == "__" {
		name = name[2:]
	}
	return name, "metamethod"
}

// getLocal returns the name and value of local variable n (1-based) in the
// given call frame. Returns ("", nil) if the local doesn't exist.
// This implements C Lua's findlocal + lua_getlocal.
func (l *State) getLocal(ci *callInfo, n int) (string, value) {
	if ci.isLua() {
		if n < 0 {
			// Access vararg values (negative index)
			p := l.stack[ci.function].(*luaClosure).prototype
			if p.isVarArg {
				base := ci.base()
				nextra := base - ci.function - 1 - p.parameterCount
				if n >= -nextra {
					// vararg at position: function + parameterCount + (-n)
					pos := ci.function + p.parameterCount - n
					return "(vararg)", l.stack[pos]
				}
			}
			return "", nil
		}
		p := l.stack[ci.function].(*luaClosure).prototype
		currentPC := ci.savedPC - 1
		if currentPC < 0 {
			currentPC = 0
		}
		name, found := p.localName(n, pc(currentPC))
		if found && n-1 >= 0 && n-1 < len(ci.frame) {
			// Lua 5.4: prefix const/close variable names with parentheses
			kind := p.localKind(n, pc(currentPC))
			if kind == varConst || kind == varToClose || kind == varCTC {
				name = "(" + name + ")"
			}
			return name, ci.frame[n-1]
		}
		// Check for temporary slots (no debug name but valid stack slot)
		if n > 0 && n <= len(ci.frame) {
			return "(temporary)", ci.frame[n-1]
		}
	} else {
		// Go/C function: locals are on the stack between function+1 and limit
		base := ci.function + 1
		var limit int
		if ci == l.callInfo {
			limit = l.top
		} else if ci.next != nil {
			limit = ci.next.function
		} else {
			limit = l.top
		}
		count := limit - base
		if n > 0 && n <= count {
			return "(C temporary)", l.stack[base+n-1]
		}
	}
	return "", nil
}

// setLocal sets the value of local variable n (1-based) in the given call frame
// to the value at the top of the stack. Pops the value from the stack.
func (l *State) setLocal(ci *callInfo, n int) {
	l.top--
	val := l.stack[l.top]
	if ci.isLua() {
		if n < 0 {
			// Set vararg value (negative index)
			p := l.stack[ci.function].(*luaClosure).prototype
			if p.isVarArg {
				base := ci.base()
				nextra := base - ci.function - 1 - p.parameterCount
				if n >= -nextra {
					pos := ci.function + p.parameterCount - n
					l.stack[pos] = val
				}
			}
		} else if n > 0 && n-1 < len(ci.frame) {
			ci.frame[n-1] = val
		}
	} else {
		base := ci.function + 1
		if n > 0 {
			l.stack[base+n-1] = val
		}
	}
}

func (l *State) collectValidLines(f closure) {
	if lc, ok := f.(*luaClosure); !ok {
		l.apiPush(nil)
	} else {
		p := lc.prototype
		t := newTable()
		l.apiPush(t)
		// Lua 5.4: lineInfo is relative deltas; resolve each PC to absolute line number.
		// For vararg functions, skip instruction 0 (VARARGPREP) — matches C Lua.
		start := 0
		if p.isVarArg {
			start = 1
		}
		for pc := start; pc < len(p.lineInfo); pc++ {
			t.putAtInt(getFuncLine(p, pc), true)
		}
	}
}

// Info gets information about a specific function or function invocation.
//
// To get information about a function invocation, the parameter where must
// be a valid activation record that was filled by a previous call to Stack
// or given as an argument to a hook (see Hook).
//
// To get information about a function you push it onto the stack and start
// the what string with the character '>'. (In that case, Info pops the
// function from the top of the stack.) For instance, to know in which line
// a function f was defined, you can write the following code:
//
//	l.Global("f") // Get global 'f'.
//	d, _ := lua.Info(l, ">S", nil)
//	fmt.Printf("%d\n", d.LineDefined)
//
// Each character in the string what selects some fields of the Debug struct
// to be filled or a value to be pushed on the stack:
//
//	'n': fills in the field Name and NameKind
//	'S': fills in the fields Source, ShortSource, LineDefined, LastLineDefined, and What
//	'l': fills in the field CurrentLine
//	't': fills in the field IsTailCall
//	'u': fills in the fields UpValueCount, ParameterCount, and IsVarArg
//	'f': pushes onto the stack the function that is running at the given level
//	'L': pushes onto the stack a table whose indices are the numbers of the lines that are valid on the function
//
// (A valid line is a line with some associated code, that is, a line where you
// can put a break point. Non-valid lines include empty lines and comments.)
//
// This function returns false on error (for instance, an invalid option in what).
func Info(l *State, what string, where Frame) (d Debug, ok bool) {
	var f closure
	var fun value
	if what[0] == '>' {
		where = nil
		fun = l.stack[l.top-1]
		switch fun := fun.(type) {
		case closure:
			f = fun
		case *goFunction:
		default:
			panic("function expected")
		}
		what = what[1:] // skip the '>'
		l.top--         // pop function
	} else {
		fun = l.stack[where.function]
		switch fun := fun.(type) {
		case closure:
			f = fun
		case *goFunction:
		default:
			l.assert(false)
		}
	}
	ok, hasL, hasF := true, false, false
	d.callInfo = where
	ci := d.callInfo
	for _, r := range what {
		switch r {
		case 'S':
			d = functionInfo(d, f)
		case 'l':
			d.CurrentLine = -1
			if where != nil && ci.isLua() {
				d.CurrentLine = l.currentLine(where)
			}
		case 'u':
			if f == nil {
				d.UpValueCount = 0
			} else {
				d.UpValueCount = f.upValueCount()
			}
			if lf, ok := f.(*luaClosure); !ok {
				d.IsVarArg = true
				d.ParameterCount = 0
			} else {
				d.IsVarArg = lf.prototype.isVarArg
				d.ParameterCount = lf.prototype.parameterCount
			}
		case 't':
			d.IsTailCall = where != nil && ci.isCallStatus(callStatusTail)
		case 'n':
			// calling function is a known Lua function?
			if where != nil && !ci.isCallStatus(callStatusTail) && where.previous.isLua() {
				d.Name, d.NameKind = l.functionName(where.previous)
			} else {
				d.NameKind = ""
			}
			if d.NameKind == "" {
				d.NameKind = "" // not found
				d.Name = ""
			}
		case 'r':
			// transfer info (ftransfer/ntransfer) - not implemented, leave as 0
		case 'L':
			hasL = true
		case 'f':
			hasF = true
		default:
			ok = false
		}
	}
	if hasF {
		l.apiPush(fun)
	}
	if hasL {
		l.collectValidLines(f)
	}
	return d, ok
}

func upValueHelper(f func(*State, int, int) (string, bool), returnValueCount int) Function {
	return func(l *State) int {
		CheckType(l, 1, TypeFunction)
		name, ok := f(l, 1, CheckInteger(l, 2))
		if !ok {
			return 0
		}
		l.PushString(name)
		l.Insert(-returnValueCount)
		return returnValueCount
	}
}

func (l *State) checkUpValue(f, upValueCount int) int {
	n := CheckInteger(l, upValueCount)
	CheckType(l, f, TypeFunction)
	l.PushValue(f)
	debug, _ := Info(l, ">u", nil)
	ArgumentCheck(l, 1 <= n && n <= debug.UpValueCount, upValueCount, "invalue upvalue index")
	return n
}

func threadArg(l *State) (int, *State) {
	if l.IsThread(1) {
		return 1, l.ToThread(1)
	}
	return 0, l
}

func hookTable(l *State) bool { return SubTable(l, RegistryIndex, "_HKEY") }

func internalHook(l *State, d Debug) {
	hookNames := []string{"call", "return", "line", "count", "tail call"}
	hookTable(l)
	l.PushThread()
	l.RawGet(-2)
	if l.IsFunction(-1) {
		l.PushString(hookNames[d.Event])
		if d.CurrentLine >= 0 {
			l.PushInteger(d.CurrentLine)
		} else {
			l.PushNil()
		}
		_, ok := Info(l, "lS", d.callInfo)
		l.assert(ok)
		l.Call(2, 0)
	}
}

func maskToString(mask byte) (s string) {
	if mask&MaskCall != 0 {
		s += "c"
	}
	if mask&MaskReturn != 0 {
		s += "r"
	}
	if mask&MaskLine != 0 {
		s += "l"
	}
	return
}

func stringToMask(s string, maskCount bool) (mask byte) {
	for r, b := range map[rune]byte{'c': MaskCall, 'r': MaskReturn, 'l': MaskLine} {
		if strings.ContainsRune(s, r) {
			mask |= b
		}
	}
	if maskCount {
		mask |= MaskCount
	}
	return
}

var debugLibrary = []RegistryFunction{
	// {"debug", db_debug},
	{"getuservalue", func(l *State) int {
		// Lua 5.4: debug.getuservalue(u, n) -> value, bool
		CheckType(l, 1, TypeUserData)
		n := OptInteger(l, 2, 1)
		if n != 1 {
			// go-lua only supports one user value per userdata
			l.PushNil()
			l.PushBoolean(false)
			return 2
		}
		l.UserValue(1)
		l.PushBoolean(true)
		return 2
	}},
	{"gethook", func(l *State) int {
		_, l1 := threadArg(l)
		hooker, mask := DebugHook(l1), DebugHookMask(l1)
		if hooker != nil && !l.internalHook {
			l.PushString("external hook")
		} else {
			hookTable(l)
			l1.PushThread()
			XMove(l1, l, 1)
			l.RawGet(-2)
			l.Remove(-2)
		}
		l.PushString(maskToString(mask))
		l.PushInteger(DebugHookCount(l1))
		return 3
	}},
	{"getinfo", func(l *State) int {
		// debug.getinfo ([thread,] f [, what])
		// f can be a function or a stack level (integer)
		// what is an optional string of options (default "flnStu")
		arg := 1
		var l1 *State
		if l.IsThread(arg) {
			l1 = l.ToThread(arg)
			arg = 2
		} else {
			l1 = l
		}

		options := OptString(l, arg+1, "flnStu")

		var ar Frame
		var d Debug
		var ok bool

		// Count how many values Info() will push (for 'f' and 'L')
		hasF := strings.Contains(options, "f")
		hasL := strings.Contains(options, "L")

		if l.IsFunction(arg) {
			// Info about a function - use ">" prefix
			l.PushValue(arg) // push function to top
			if l1 != l {
				XMove(l, l1, 1) // move function to l1
			}
			d, ok = Info(l1, ">"+options, nil)
			if l1 != l && (hasF || hasL) {
				// Move pushed values back to l
				count := 0
				if hasF {
					count++
				}
				if hasL {
					count++
				}
				XMove(l1, l, count)
			}
			if !ok {
				ArgumentError(l, arg+1, "invalid option")
			}
		} else {
			// Stack level
			level := CheckInteger(l, arg)
			ar, ok = Stack(l1, level)
			if !ok {
				l.PushNil() // level out of range
				return 1
			}
			d, ok = Info(l1, options, ar)
			if l1 != l && (hasF || hasL) {
				// Move pushed values back to l
				count := 0
				if hasF {
					count++
				}
				if hasL {
					count++
				}
				XMove(l1, l, count)
			}
			if !ok {
				ArgumentError(l, arg+1, "invalid option")
			}
		}

		// Info() pushes 'f' first, then 'L' (if requested)
		// Stack after Info(): ... [func] [activelines]
		// We need to save these before creating the result table

		// Create result table
		l.CreateTable(0, 12)
		resultIdx := l.Top() // index of result table

		if strings.Contains(options, "S") {
			l.PushString(d.Source)
			l.SetField(resultIdx, "source")
			l.PushString(d.ShortSource)
			l.SetField(resultIdx, "short_src")
			l.PushInteger(d.LineDefined)
			l.SetField(resultIdx, "linedefined")
			l.PushInteger(d.LastLineDefined)
			l.SetField(resultIdx, "lastlinedefined")
			l.PushString(d.What)
			l.SetField(resultIdx, "what")
		}
		if strings.Contains(options, "l") {
			l.PushInteger(d.CurrentLine)
			l.SetField(resultIdx, "currentline")
		}
		if strings.Contains(options, "u") {
			l.PushInteger(d.UpValueCount)
			l.SetField(resultIdx, "nups")
			l.PushInteger(d.ParameterCount)
			l.SetField(resultIdx, "nparams")
			l.PushBoolean(d.IsVarArg)
			l.SetField(resultIdx, "isvararg")
		}
		if strings.Contains(options, "n") {
			if d.Name != "" {
				l.PushString(d.Name)
			} else {
				l.PushNil()
			}
			l.SetField(resultIdx, "name")
			l.PushString(d.NameKind)
			l.SetField(resultIdx, "namewhat")
		}
		if strings.Contains(options, "t") {
			l.PushBoolean(d.IsTailCall)
			l.SetField(resultIdx, "istailcall")
		}
		if strings.Contains(options, "r") {
			l.PushInteger(d.FTransfer)
			l.SetField(resultIdx, "ftransfer")
			l.PushInteger(d.NTransfer)
			l.SetField(resultIdx, "ntransfer")
		}

		// 'f' and 'L' values were pushed by Info() before the result table
		// Stack: ... [func?] [activelines?] [result_table]
		// We need to move them into the result table
		if hasL {
			// activelines is at resultIdx-1 (or resultIdx-2 if hasF)
			idx := resultIdx - 1
			if hasF {
				idx = resultIdx - 1
			}
			l.PushValue(idx)
			l.SetField(resultIdx, "activelines")
		}
		if hasF {
			// func is at resultIdx-1 (or resultIdx-2 if hasL)
			idx := resultIdx - 1
			if hasL {
				idx = resultIdx - 2
			}
			l.PushValue(idx)
			l.SetField(resultIdx, "func")
		}

		// Move result table to correct position and clean up
		// Stack: ... [func?] [activelines?] [result_table]
		if hasF || hasL {
			extra := 0
			if hasF {
				extra++
			}
			if hasL {
				extra++
			}
			// Move result_table down over extra values, then pop leftovers
			l.Replace(resultIdx - extra)
			for i := 1; i < extra; i++ {
				l.Pop(1)
			}
		}

		return 1
	}},
	{"getlocal", func(l *State) int {
		// debug.getlocal ([thread,] f, local)
		arg := 1
		var l1 *State
		if l.IsThread(arg) {
			l1 = l.ToThread(arg)
			arg = 2 // skip thread argument
		} else {
			l1 = l
		}

		if l.IsFunction(arg) {
			// Non-active function: return parameter names only
			l.PushValue(arg)
			f := l.stack[l.top-1]
			l.top--
			cl, ok := f.(*luaClosure)
			if !ok {
				l.PushNil()
				return 1
			}
			n := CheckInteger(l, arg+1)
			name, found := cl.prototype.localName(n, 0)
			if !found {
				l.PushNil()
				return 1
			}
			l.PushString(name)
			return 1
		}

		// Stack level
		level := CheckInteger(l, arg)
		n := CheckInteger(l, arg+1)

		ar, ok := Stack(l1, level)
		if !ok {
			ArgumentError(l, arg, "level out of range")
			return 0
		}

		name, val := l1.getLocal(ar, n)
		if name == "" {
			l.PushNil()
			return 1
		}
		l.PushString(name)
		l.push(val)
		return 2
	}},
	{"getregistry", func(l *State) int { l.PushValue(RegistryIndex); return 1 }},
	{"getmetatable", func(l *State) int {
		CheckAny(l, 1)
		if !l.MetaTable(1) {
			l.PushNil()
		}
		return 1
	}},
	{"getupvalue", upValueHelper(UpValue, 2)},
	{"upvaluejoin", func(l *State) int {
		n1 := l.checkUpValue(1, 2)
		n2 := l.checkUpValue(3, 4)
		ArgumentCheck(l, !l.IsGoFunction(1), 1, "Lua function expected")
		ArgumentCheck(l, !l.IsGoFunction(3), 3, "Lua function expected")
		UpValueJoin(l, 1, n1, 3, n2)
		return 0
	}},
	{"upvalueid", func(l *State) int { l.PushLightUserData(UpValueId(l, 1, l.checkUpValue(1, 2))); return 1 }},
	{"setuservalue", func(l *State) int {
		// Lua 5.4: debug.setuservalue(u, value, n) -> u, bool
		CheckType(l, 1, TypeUserData)
		CheckAny(l, 2)
		n := OptInteger(l, 3, 1)
		l.SetTop(3) // ensure 3 slots
		if n != 1 {
			// go-lua only supports one user value per userdata
			l.SetTop(1) // return just the userdata
			l.PushBoolean(false)
			return 2
		}
		l.SetTop(2)
		l.SetUserValue(1)
		l.PushBoolean(true)
		return 2
	}},
	{"sethook", func(l *State) int {
		var hook Hook
		var mask byte
		var count int
		i, l1 := threadArg(l)
		if l.IsNoneOrNil(i + 1) {
			l.SetTop(i + 1)
		} else {
			s := CheckString(l, i+2)
			CheckType(l, i+1, TypeFunction)
			count = OptInteger(l, i+3, 0)
			hook, mask = internalHook, stringToMask(s, count > 0)
		}
		if !hookTable(l) {
			l.PushString("k")
			l.SetField(-2, "__mode")
			l.PushValue(-1)
			l.SetMetaTable(-2)
		}
		l1.PushThread()
		XMove(l1, l, 1)
		l.PushValue(i + 1)
		l.RawSet(-3)
		SetDebugHook(l1, hook, mask, count)
		l1.internalHook = true
		return 0
	}},
	{"setcstacklimit", func(l *State) int {
		// Lua 5.4: set C stack limit. Go doesn't have a C stack, so always
		// return 0 (indicating failure, as in C Lua when the limit is invalid).
		CheckInteger(l, 1)
		l.PushInteger(0)
		return 1
	}},
	{"setlocal", func(l *State) int {
		// debug.setlocal ([thread,] level, local, value)
		arg := 1
		var l1 *State
		if l.IsThread(arg) {
			l1 = l.ToThread(arg)
			arg = 2
		} else {
			l1 = l
		}
		level := CheckInteger(l, arg)
		n := CheckInteger(l, arg+1)
		CheckAny(l, arg+2)
		ar, ok := Stack(l1, level)
		if !ok {
			ArgumentError(l, arg, "level out of range")
			return 0
		}
		name, _ := l1.getLocal(ar, n)
		if name == "" {
			l.PushNil()
			return 0
		}
		// Check if variable is read-only (const/close)
		if name == "(const)" || name == "(close)" {
			ArgumentError(l, arg+1, "constant or to-be-closed variable")
		}
		// Set the value — move value to l1 if needed
		l.SetTop(arg + 2)
		if l1 != l {
			XMove(l, l1, 1) // move value to l1
		}
		l1.setLocal(ar, n)
		l.PushString(name)
		return 1
	}},
	{"setmetatable", func(l *State) int {
		t := l.TypeOf(2)
		ArgumentCheck(l, t == TypeNil || t == TypeTable, 2, "nil or table expected")
		l.SetTop(2)
		l.SetMetaTable(1)
		return 1
	}},
	{"setupvalue", upValueHelper(SetUpValue, 1)},
	{"traceback", func(l *State) int {
		i, l1 := threadArg(l)
		if s, ok := l.ToString(i + 1); !ok && !l.IsNoneOrNil(i+1) {
			l.PushValue(i + 1)
		} else if l == l1 {
			Traceback(l, l, s, OptInteger(l, i+2, 1))
		} else {
			Traceback(l, l1, s, OptInteger(l, i+2, 0))
		}
		return 1
	}},
}

// DebugOpen opens the debug library. Usually passed to Require.
func DebugOpen(l *State) int {
	NewLibrary(l, debugLibrary)
	return 1
}
