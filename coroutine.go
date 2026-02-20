package lua

var coroutineLibrary = []RegistryFunction{
	{"close", func(l *State) int {
		co := CheckThread(l, 1)
		// Cannot close a running coroutine
		if co == l {
			Errorf(l, "cannot close a running coroutine")
		}
		// Cannot close a normal coroutine (one that has resumed another)
		if co.status == threadStatusOK && co.callInfo != &co.baseCallInfo {
			Errorf(l, "cannot close a normal coroutine")
		}
		// Like C Lua's luaE_resetthread: reset coroutine state and close TBC vars
		hadError := co.hasError
		co.hasError = false
		// Reset call info to base (like C Lua)
		co.callInfo = &co.baseCallInfo
		co.errorFunction = 0 // clear any xpcall error handler
		co.status = threadStatusOK // temporarily OK so __close handlers can run
		// Close TBC variables in protected mode with error chaining
		closeErrVal := co.closeTBCProtected(0, nil)
		// Mark it dead
		co.status = threadStatusDead
		if closeErrVal != nil {
			// __close handler threw an error
			l.PushBoolean(false)
			l.push(closeErrVal)
			return 2
		}
		if hadError {
			// Coroutine died with an error — return false + error value
			l.PushBoolean(false)
			if co.Top() > 0 {
				XMove(co, l, 1)
			} else {
				l.PushNil()
			}
			co.top = 1
			return 2
		}
		// Clean close
		co.top = 1
		l.PushBoolean(true)
		return 1
	}},
	{"create", func(l *State) int {
		CheckType(l, 1, TypeFunction)
		co := l.NewThread()
		l.PushValue(1)   // push function
		XMove(l, co, 1)  // move function to coroutine stack
		return 1         // return the thread
	}},
	{"resume", coroutineResume},
	{"yield", func(l *State) int {
		return l.Yield(l.Top())
	}},
	{"status", func(l *State) int {
		co := CheckThread(l, 1)
		if l == co {
			l.PushString("running")
		} else if co.status == threadStatusYield {
			l.PushString("suspended")
		} else if co.status == threadStatusDead {
			l.PushString("dead")
		} else if co.caller != nil {
			// co is OK and has a caller: it called resume on someone else
			l.PushString("normal")
		} else if co.status == threadStatusOK && co.callInfo == &co.baseCallInfo {
			// Never started (has function on stack) or already finished
			if co.top > 1 {
				l.PushString("suspended")
			} else {
				l.PushString("dead")
			}
		} else {
			l.PushString("dead")
		}
		return 1
	}},
	{"wrap", func(l *State) int {
		CheckType(l, 1, TypeFunction)
		l.NewThread()
		l.PushValue(1)                  // push function
		XMove(l, l.ToThread(-2), 1)     // move function to coroutine stack
		l.PushGoClosure(coroutineWrapHelper, 1)
		return 1
	}},
	{"running", func(l *State) int {
		isMain := l.PushThread()
		l.PushBoolean(isMain)
		return 2
	}},
	{"isyieldable", func(l *State) int {
		// Lua 5.4: optional argument (coroutine to check)
		if l.Top() >= 1 && l.TypeOf(1) == TypeThread {
			co := l.ToThread(1)
			l.PushBoolean(co.nonYieldableCallCount == 0)
		} else {
			l.PushBoolean(l.nonYieldableCallCount == 0)
		}
		return 1
	}},
}

func coroutineResume(l *State) int {
	co := CheckThread(l, 1)
	nArgs := l.Top() - 1

	// Move arguments from caller to coroutine stack
	if nArgs > 0 {
		if !co.CheckStack(nArgs) {
			l.PushBoolean(false)
			l.PushString("too many arguments to resume")
			return 2
		}
		XMove(l, co, nArgs) // moves top nArgs values from l to co
	}
	l.Pop(1) // remove the coroutine from the caller's stack

	err := co.Resume(l, nArgs)
	if err != nil {
		// Error: push false + error message
		l.PushBoolean(false)
		// Get error message from coroutine stack
		if co.Top() > 0 {
			co.PushValue(-1) // copy error to top
			XMove(co, l, 1)
		} else {
			l.PushString(err.Error())
		}
		return 2
	}

	// Success: push true + results from coroutine
	nResults := co.Top()
	if !l.CheckStack(nResults + 1) {
		co.SetTop(0)
		l.PushBoolean(false)
		l.PushString("too many results to resume")
		return 2
	}
	l.PushBoolean(true)
	if nResults > 0 {
		XMove(co, l, nResults)
	}
	return nResults + 1
}

func coroutineWrapHelper(l *State) int {
	co := l.ToThread(UpValueIndex(1))
	nArgs := l.Top()

	// Move arguments to coroutine
	if nArgs > 0 {
		co.CheckStack(nArgs)
		XMove(l, co, nArgs)
	}

	err := co.Resume(l, nArgs)
	if err != nil {
		// Close dead coroutine's TBC variables (like C Lua's lua_closethread)
		if co.status == threadStatusDead {
			// Save error value before reset
			var errObj value
			if co.top > 1 {
				errObj = co.stack[co.top-1]
			}
			// Reset coroutine state (like luaE_resetthread)
			co.callInfo = &co.baseCallInfo
			co.errorFunction = 0
			co.status = threadStatusOK // temporarily so __close handlers can run
			co.closeUpValues(1)
			closeErr := co.closeTBCProtected(1, errObj)
			// Set error on co's stack at position 1
			if closeErr != nil {
				co.stack[1] = closeErr
			} else {
				co.stack[1] = errObj
			}
			co.top = 2
			co.status = threadStatusDead
		}
		// Propagate error
		if co.Top() > 0 {
			co.PushValue(-1)
			XMove(co, l, 1)
		} else {
			l.PushString(err.Error())
		}
		l.Error()
		return 0
	}

	// Return results
	nResults := co.Top()
	if nResults > 0 {
		if !l.CheckStack(nResults) {
			l.push("too many results")
			l.Error()
		}
		XMove(co, l, nResults)
	}
	return nResults
}

// CheckThread checks whether the value at index is a thread and returns it.
func CheckThread(l *State, index int) *State {
	if co := l.ToThread(index); co != nil {
		return co
	}
	tagError(l, index, TypeThread)
	return nil
}

// CoroutineOpen opens the coroutine library. Usually passed to Require.
func CoroutineOpen(l *State) int {
	NewLibrary(l, coroutineLibrary)
	return 1
}
