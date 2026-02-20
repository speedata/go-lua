package lua

import (
	"fmt"
	"log"
)

func (l *State) push(v value) {
	l.stack[l.top] = v
	l.top++
}

func (l *State) pop() value {
	l.top--
	return l.stack[l.top]
}

type upValue struct {
	home interface{}
}

type closure interface {
	upValue(i int) value
	setUpValue(i int, v value)
	upValueCount() int
}

type luaClosure struct {
	prototype *prototype
	upValues  []*upValue
}

type goClosure struct {
	function Function
	upValues []value
}

// Function wrapper, to allow go functions as keys in maps. Explicitly not a closure.
type goFunction struct {
	Function
}

func (c *luaClosure) upValue(i int) value {
	uv := c.upValues[i]
	if home, ok := uv.home.(stackLocation); ok {
		return home.state.stack[home.index]
	}
	return uv.home
}

func (c *luaClosure) setUpValue(i int, v value) {
	uv := c.upValues[i]
	if home, ok := uv.home.(stackLocation); ok {
		home.state.stack[home.index] = v
	} else {
		uv.home = v
	}
}

func (c *luaClosure) upValueCount() int        { return len(c.upValues) }
func (c *goClosure) upValue(i int) value       { return c.upValues[i] }
func (c *goClosure) setUpValue(i int, v value) { c.upValues[i] = v }
func (c *goClosure) upValueCount() int         { return len(c.upValues) }
func (l *State) newUpValue() *upValue          { return &upValue{home: nil} }

func (uv *upValue) value() value {
	if home, ok := uv.home.(stackLocation); ok {
		return home.state.stack[home.index]
	}
	return uv.home
}

func (uv *upValue) close() {
	if home, ok := uv.home.(stackLocation); ok {
		uv.home = home.state.stack[home.index]
	} else {
		panic("attempt to close already-closed up value")
	}
}

func (uv *upValue) isInStackAt(level int) bool {
	if home, ok := uv.home.(stackLocation); ok {
		return home.index == level
	}
	return false
}

func (uv *upValue) isInStackAbove(level int) bool {
	if home, ok := uv.home.(stackLocation); ok {
		return home.index >= level
	}
	return false
}

type openUpValue struct {
	upValue *upValue
	next    *openUpValue
}

func (l *State) newUpValueAt(level int) *upValue {
	uv := &upValue{home: stackLocation{state: l, index: level}}
	l.upValues = &openUpValue{upValue: uv, next: l.upValues}
	return uv
}

func (l *State) close(level int) {
	l.closeUpValues(level)
	l.closeTBC(level)
}

// closeWithError closes upvalues and TBC variables, passing errObj to __close handlers.
func (l *State) closeWithError(level int, errObj value) {
	l.closeUpValues(level)
	l.closeTBCWithErr(level, errObj, false)
}

func (l *State) closeUpValues(level int) {
	// TODO this seems really inefficient - how can we terminate early?
	var p *openUpValue
	for e := l.upValues; e != nil; e, p = e.next, e {
		if e.upValue.isInStackAbove(level) {
			e.upValue.close()
			if p != nil {
				p.next = e.next
			} else {
				l.upValues = e.next
			}
		}
	}
}

// newTBCUpValue registers a stack index as a to-be-closed variable.
func (l *State) newTBCUpValue(level int) {
	l.tbcList = append(l.tbcList, level)
}

// closeTBC calls __close metamethods for to-be-closed variables at or above level.
// errObj is passed as the error argument to each handler (nil for normal close).
// If a handler throws, the error propagates normally.
func (l *State) closeTBC(level int) {
	l.closeTBCWithErr(level, nil, false)
}

// closeTBCWithErr calls __close metamethods passing errObj to each handler.
// If yieldable is true, the __close handlers may yield (for use inside coroutines).
func (l *State) closeTBCWithErr(level int, errObj value, yieldable bool) {
	for len(l.tbcList) > 0 {
		idx := l.tbcList[len(l.tbcList)-1]
		if idx < level {
			break
		}
		l.tbcList = l.tbcList[:len(l.tbcList)-1]
		obj := l.stack[idx]
		if obj == nil || obj == false {
			continue
		}
		tm := l.tagMethodByObject(obj, tmClose)
		// Push and call even if tm is nil — this matches C Lua behavior
		// and will produce "attempt to call a nil value" with proper debug info.
		l.push(tm)
		l.push(obj)
		l.push(errObj) // error object (nil for normal close, or actual error)
		l.call(l.top-3, 0, yieldable)
	}
}

// closeYieldable closes upvalues and TBC variables, allowing __close handlers to yield.
// Used by opClose, opReturn, opReturn0, opReturn1 inside coroutines.
func (l *State) closeYieldable(level int) {
	l.closeUpValues(level)
	l.closeTBCWithErr(level, nil, true)
}

// closeTBCProtected calls __close metamethods in protected mode with error chaining.
// Like C Lua's luaD_closeprotected: if a handler throws, the error is caught,
// passed to subsequent handlers, and the final error value is returned.
// initialErr is the error that triggered the close (nil for normal close).
func (l *State) closeTBCProtected(level int, initialErr value) (finalErr value) {
	errObj := initialErr
	for len(l.tbcList) > 0 {
		idx := l.tbcList[len(l.tbcList)-1]
		if idx < level {
			break
		}
		l.tbcList = l.tbcList[:len(l.tbcList)-1]
		obj := l.stack[idx]
		if obj == nil || obj == false {
			continue
		}
		tm := l.tagMethodByObject(obj, tmClose)
		// Call even if tm is nil — matches C Lua behavior where callclosemethod
		// pushes tm unconditionally. If nil/non-callable, the call will error.
		savedCI := l.callInfo
		savedTop := l.top
		callErr := l.protect(func() {
			l.push(tm)
			l.push(obj)
			l.push(errObj) // pass current error (nil initially, or chained error)
			l.call(l.top-3, 0, false)
		})
		if callErr != nil {
			// Handler threw — error value is at l.stack[l.top-1]
			// Extract it before restoring state
			if l.top > savedTop {
				errObj = l.stack[l.top-1]
			}
			l.callInfo = savedCI
			l.top = savedTop
		}
	}
	return errObj
}

// information about a call
type callInfo struct {
	function, top, resultCount int
	previous, next             *callInfo
	callStatus                 callStatus
	*luaCallInfo
	*goCallInfo
}

type luaCallInfo struct {
	frame    []value
	savedPC  pc
	code     []instruction
	savedTop int // l.top saved before TBC close (for yield-resume with b==0)
}

type goCallInfo struct {
	context, extra, oldErrorFunction int
	continuation                     Function
	oldAllowHook, shouldYield        bool
	error                            error
	recoverStatus                    error // error status during pcall TBC close recovery (like C Lua's CIST_RECST)
	recoverErrObj                    value // error value to pass to __close handlers during recovery
}

func (ci *callInfo) setCallStatus(flag callStatus)     { ci.callStatus |= flag }
func (ci *callInfo) clearCallStatus(flag callStatus)   { ci.callStatus &^= flag }
func (ci *callInfo) isCallStatus(flag callStatus) bool { return ci.callStatus&flag != 0 }
func (ci *callInfo) isLua() bool                       { return ci.luaCallInfo != nil }

func (ci *callInfo) stackIndex(slot int) int { return ci.top - len(ci.frame) + slot }
func (ci *callInfo) base() int               { return ci.top - len(ci.frame) }
func (ci *callInfo) skip()                   { ci.savedPC++ }
func (ci *callInfo) jump(offset int)         { ci.savedPC += pc(offset) }

func (ci *callInfo) setTop(top int) {
	if ci.luaCallInfo != nil {
		diff := top - ci.top
		ci.frame = ci.frame[:len(ci.frame)+diff]
	}
	ci.top = top
}

func (ci *callInfo) frameIndex(stackSlot int) int {
	if stackSlot < ci.top-len(ci.frame) || ci.top <= stackSlot {
		panic("frameIndex called with out-of-range stackSlot")
	}
	return stackSlot - ci.top + len(ci.frame)
}

func (l *State) pushLuaFrame(function, base, resultCount int, p *prototype) *callInfo {
	ci := l.callInfo.next
	if ci == nil {
		ci = &callInfo{previous: l.callInfo, luaCallInfo: &luaCallInfo{code: p.code}}
		l.callInfo.next = ci
	} else if ci.luaCallInfo == nil {
		ci.goCallInfo = nil
		ci.luaCallInfo = &luaCallInfo{code: p.code}
	} else {
		ci.savedPC = 0
		ci.code = p.code
	}
	ci.function = function
	ci.top = base + p.maxStackSize
	// TODO l.assert(ci.top <= l.stackLast)
	ci.resultCount = resultCount
	ci.callStatus = callStatusLua
	ci.frame = l.stack[base:ci.top]
	l.callInfo = ci
	l.top = ci.top
	return ci
}

func (l *State) pushGoFrame(function, resultCount int) {
	ci := l.callInfo.next
	if ci == nil {
		ci = &callInfo{previous: l.callInfo, goCallInfo: &goCallInfo{}}
		l.callInfo.next = ci
	} else if ci.goCallInfo == nil {
		ci.goCallInfo = &goCallInfo{}
		ci.luaCallInfo = nil
	}
	ci.function = function
	ci.top = l.top + MinStack
	// TODO l.assert(ci.top <= l.stackLast)
	ci.resultCount = resultCount
	ci.callStatus = 0
	l.callInfo = ci
}

func (ci *luaCallInfo) step() instruction {
	i := ci.code[ci.savedPC]
	ci.savedPC++
	return i
}

func (l *State) newLuaClosure(p *prototype) *luaClosure {
	return &luaClosure{prototype: p, upValues: make([]*upValue, len(p.upValues))}
}

func (l *State) findUpValue(level int) *upValue {
	for e := l.upValues; e != nil; e = e.next {
		if e.upValue.isInStackAt(level) {
			return e.upValue
		}
	}
	return l.newUpValueAt(level)
}

func (l *State) newClosure(p *prototype, upValues []*upValue, base int) value {
	c := l.newLuaClosure(p)
	p.cache = c
	for i, uv := range p.upValues {
		if uv.isLocal { // upValue refers to local variable
			c.upValues[i] = l.findUpValue(base + uv.index)
		} else { // get upValue from enclosing function
			c.upValues[i] = upValues[uv.index]
		}
	}
	return c
}

func cached(p *prototype, upValues []*upValue, base int) *luaClosure {
	c := p.cache
	if c != nil {
		for i, uv := range p.upValues {
			if uv.isLocal && !c.upValues[i].isInStackAt(base+uv.index) {
				return nil
			} else if !uv.isLocal && c.upValues[i].home != upValues[uv.index].home {
				return nil
			}
		}
	}
	return c
}

func (l *State) callGo(f value, function int, resultCount int) {
	l.checkStack(MinStack)
	l.pushGoFrame(function, resultCount)
	if l.hookMask&MaskCall != 0 {
		l.hook(HookCall, -1)
	}
	var n int
	switch f := f.(type) {
	case *goClosure:
		n = f.function(l)
	case *goFunction:
		n = f.Function(l)
	}
	apiCheckStackSpace(l, n)
	l.postCall(l.top - n)
}

func (l *State) preCall(function int, resultCount int) bool {
	for {
		switch f := l.stack[function].(type) {
		case *goClosure:
			l.callGo(f, function, resultCount)
			return true
		case *goFunction:
			l.callGo(f, function, resultCount)
			return true
		case *luaClosure:
			p := f.prototype
			l.checkStack(p.maxStackSize)
			argCount, parameterCount := l.top-function-1, p.parameterCount
			if argCount < parameterCount {
				extra := parameterCount - argCount
				args := l.stack[l.top : l.top+extra]
				for i := range args {
					args[i] = nil
				}
				l.top += extra
				argCount += extra
			}
			base := function + 1
			if p.isVarArg {
				base = l.adjustVarArgs(p, argCount)
			}
			ci := l.pushLuaFrame(function, base, resultCount, p)
			if l.hookMask != 0 && !p.isVarArg {
				// For non-vararg functions: set oldpc and call hook now
				// (matches luaG_tracecall → luaD_hookcall)
				l.oldPC = 0
				if l.hookMask&MaskCall != 0 {
					l.callHook(ci)
				}
			}
			// For vararg functions, hook setup is deferred to opVarArgPrep
			return false
		default:
			tm := l.tagMethodByObject(f, tmCall)

			if tm == nil {
				l.typeErrorAt(function, "call")
			}
			// Slide the args + function up 1 slot and poke in the tag method
			for p := l.top; p > function; p-- {
				l.stack[p] = l.stack[p-1]
			}
			l.top++
			l.checkStack(0)
			l.stack[function] = tm
		}
	}
}

func (l *State) callHook(ci *callInfo) {
	ci.savedPC++ // hooks assume 'pc' is already incremented
	if pci := ci.previous; pci.isLua() && pci.savedPC > 0 && len(pci.code) > 0 && pci.code[pci.savedPC-1].opCode() == opTailCall {
		ci.setCallStatus(callStatusTail)
		l.hook(HookTailCall, -1)
	} else {
		l.hook(HookCall, -1)
	}
	ci.savedPC-- // correct 'pc'
}

func (l *State) adjustVarArgs(p *prototype, argCount int) int {
	fixedArgCount := p.parameterCount
	l.assert(argCount >= fixedArgCount)
	// move fixed parameters to final position
	fixed := l.top - argCount // first fixed argument
	base := l.top             // final position of first argument
	// Ensure we have enough stack space for the fixed args at the new position
	l.checkStack(fixedArgCount)
	fixedArgs := l.stack[fixed : fixed+fixedArgCount]
	copy(l.stack[base:base+fixedArgCount], fixedArgs)
	for i := range fixedArgs {
		fixedArgs[i] = nil
	}
	return base
}

func (l *State) postCall(firstResult int) bool {
	ci := l.callInfo
	if l.hookMask&MaskReturn != 0 {
		l.hook(HookReturn, -1)
	}
	result, wanted, i := ci.function, ci.resultCount, 0
	l.callInfo = ci.previous // back to caller
	// TODO this is obscure - I don't fully understand the control flow, but it works
	for i = wanted; i != 0 && firstResult < l.top; i-- {
		l.stack[result] = l.stack[firstResult]
		result++
		firstResult++
	}
	for ; i > 0; i-- {
		l.stack[result] = nil
		result++
	}
	l.top = result
	if l.hookMask&(MaskReturn|MaskLine) != 0 && l.callInfo.isLua() {
		// Match C Lua rethook: pcRel(savedpc) = savedpc_index - 1
		// This makes oldPC point to the CALL instruction itself, so the
		// next traceExecution won't fire a spurious line hook for the
		// same line as the CALL.
		l.oldPC = l.callInfo.savedPC - 1 // oldPC for caller function
	}
	return wanted != MultipleReturns
}

// Call a Go or Lua function. The function to be called is at function.
// The arguments are on the stack, right after the function. On return, all the
// results are on the stack, starting at the original function position.
func (l *State) call(function int, resultCount int, allowYield bool) {
	if l.nestedGoCallCount++; l.nestedGoCallCount == maxCallCount {
		l.runtimeError("Go stack overflow")
	} else if l.nestedGoCallCount >= maxCallCount+maxCallCount>>3 {
		l.throw(ErrorError) // error while handling stack error
	}
	if !allowYield {
		l.nonYieldableCallCount++
	}
	if !l.preCall(function, resultCount) { // is a Lua function?
		l.execute() // call it
	}
	if !allowYield {
		l.nonYieldableCallCount--
	}
	l.nestedGoCallCount--
}

func (l *State) throw(errorCode error) {
	if l.protectFunction != nil {
		panic(errorCode)
	} else {
		l.error = errorCode
		if g := l.global.mainThread; g.protectFunction != nil {
			g.push(l.stack[l.top-1])
			g.throw(errorCode)
		} else {
			if l.global.panicFunction != nil {
				l.global.panicFunction(l)
			}
			log.Panicf("Uncaught Lua error: %v", errorCode)
		}
	}
}

func (l *State) protect(f func()) (err error) {
	nestedGoCallCount, protectFunction := l.nestedGoCallCount, l.protectFunction
	l.protectFunction = func() {
		if e := recover(); e != nil {
			// Let yield errors propagate through to Resume's recover
			if e == yieldError {
				panic(e)
			}
			if errVal, ok := e.(error); ok {
				err = errVal
			} else {
				// Handle non-error panics (e.g., strings)
				err = fmt.Errorf("%v", e)
			}
			l.nestedGoCallCount, l.protectFunction = nestedGoCallCount, protectFunction
		}
	}
	defer l.protectFunction()
	f()
	l.nestedGoCallCount, l.protectFunction = nestedGoCallCount, protectFunction
	return err
}

func (l *State) hook(event, line int) {
	if l.hooker == nil || !l.allowHook {
		return
	}
	ci := l.callInfo
	top := l.top
	ciTop := ci.top
	ar := Debug{Event: event, CurrentLine: line, callInfo: ci}
	l.checkStack(MinStack)
	ci.setTop(l.top + MinStack)
	l.assert(ci.top <= l.stackLast)
	l.allowHook = false // can't hook calls inside a hook
	ci.setCallStatus(callStatusHooked)
	l.hooker(l, ar)
	l.assert(!l.allowHook)
	l.allowHook = true
	ci.setTop(ciTop)
	l.top = top
	ci.clearCallStatus(callStatusHooked)
}

func (l *State) initializeStack() {
	l.stack = make([]value, basicStackSize)
	l.stackLast = basicStackSize - extraStack
	l.top++
	l.baseCallInfo.luaCallInfo = &luaCallInfo{frame: l.stack[:0]}
	l.baseCallInfo.setTop(l.top + MinStack)
	l.callInfo = &l.baseCallInfo
}

func (l *State) checkStack(n int) {
	if l.stackLast-l.top <= n {
		l.growStack(n)
	}
}

func (l *State) reallocStack(newSize int) {
	l.assert(newSize <= maxStack || newSize == errorStackSize)
	oldSize := len(l.stack)
	if newSize > oldSize {
		l.stack = append(l.stack, make([]value, newSize-oldSize)...)
	} else if newSize < oldSize {
		// Clear references in the truncated portion to allow GC
		for i := newSize; i < oldSize; i++ {
			l.stack[i] = nil
		}
		l.stack = l.stack[:newSize]
	}
	l.stackLast = len(l.stack) - extraStack
	l.callInfo.next = nil
	for ci := l.callInfo; ci != nil; ci = ci.previous {
		if ci.isLua() {
			top := ci.top
			ci.frame = l.stack[top-len(ci.frame) : top]
		}
	}
}

func (l *State) stackInUse() int {
	maxTop := l.top
	for ci := l.callInfo; ci != nil; ci = ci.previous {
		if ci.top > maxTop {
			maxTop = ci.top
		}
	}
	return maxTop + 1 + extraStack
}

func (l *State) shrinkStack() {
	inUse := l.stackInUse()
	goodSize := inUse + inUse/8 + 2*extraStack
	if goodSize > maxStack {
		goodSize = maxStack
	}
	if len(l.stack) > maxStack { // was handling stack overflow?
		l.callInfo.next = nil // free extra callInfo chain
	}
	if inUse <= maxStack-extraStack && goodSize < len(l.stack) {
		l.reallocStack(goodSize)
	}
}

func (l *State) growStack(n int) {
	if len(l.stack) > maxStack { // error after extra size?
		l.throw(ErrorError)
	} else {
		needed := l.top + n + extraStack
		newSize := 2 * len(l.stack)
		if newSize > maxStack {
			newSize = maxStack
		}
		if newSize < needed {
			newSize = needed
		}
		if newSize > maxStack { // stack overflow?
			l.reallocStack(errorStackSize)
			l.runtimeError("stack overflow")
		} else {
			l.reallocStack(newSize)
		}
	}
}
