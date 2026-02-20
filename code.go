package lua

import (
	"fmt"
	"math"
)

const (
	oprMinus = iota
	oprBNot  // Lua 5.3: bitwise NOT ~
	oprNot
	oprLength
	oprNoUnary
)

const (
	noJump            = -1
	noRegister        = maxArgA
	maxLocalVariables = 200
)

// Variable declaration kinds (Lua 5.4 attributes)
const (
	varRegular = 0 // VDKREG: regular variable
	varConst   = 1 // RDKCONST: <const> variable
	varToClose = 2 // RDKTOCLOSE: <close> variable
	varCTC     = 3 // RDKCTC: compile-time constant
)

const (
	oprAdd = iota
	oprSub
	oprMul
	oprMod // Lua 5.3: MOD before DIV
	oprPow
	oprDiv
	oprIDiv // Lua 5.3: integer division //
	oprBAnd // Lua 5.3: bitwise AND &
	oprBOr  // Lua 5.3: bitwise OR |
	oprBXor // Lua 5.3: bitwise XOR ~
	oprShl  // Lua 5.3: shift left <<
	oprShr  // Lua 5.3: shift right >>
	oprConcat
	oprEq
	oprLT
	oprLE
	oprNE
	oprGT
	oprGE
	oprAnd
	oprOr
	oprNoBinary
)

const (
	kindVoid = iota // no value
	kindNil
	kindTrue
	kindFalse
	kindConstant       // info = index of constant
	kindNumber         // value = numerical value
	kindInteger        // ivalue = integer value (Lua 5.3)
	kindString         // strVal = string value (Lua 5.4)
	kindNonRelocatable // info = result register
	kindLocal          // info = local register
	kindUpValue        // info = index of upvalue
	kindIndexed        // table = register, index = register
	kindIndexUp        // table = upvalue index, index = string constant index
	kindIndexInt       // table = register, index = integer key
	kindIndexStr       // table = register, index = string constant index
	kindJump           // info = instruction pc
	kindRelocatable    // info = instruction pc
	kindCall           // info = instruction pc
	kindVarArg         // info = instruction pc
)

var kinds []string = []string{
	"void",
	"nil",
	"true",
	"false",
	"constant",
	"number",
	"integer",
	"string",
	"nonrelocatable",
	"local",
	"upvalue",
	"indexed",
	"indexup",
	"indexint",
	"indexstr",
	"jump",
	"relocatable",
	"call",
	"vararg",
}

type exprDesc struct {
	kind    int
	index   int // register/constant index
	table   int // register or upvalue
	info    int
	t, f    int     // patch lists for 'exit when true/false'
	value   float64 // for kindNumber
	ivalue  int64   // for kindInteger (Lua 5.3)
	strVal  string  // for kindString (Lua 5.4)
	ctcName string  // variable name for CTC constants (for checkReadOnly error messages)
}

type assignmentTarget struct {
	previous *assignmentTarget
	exprDesc
}

type label struct {
	name                string
	pc, line            int
	activeVariableCount int
	close               bool // 5.4: needs CLOSE when goto is resolved
}

type block struct {
	previous              *block
	firstLabel, firstGoto int
	activeVariableCount   int
	hasUpValue, isLoop    bool
	insidetbc             bool // Lua 5.4: inside scope of TBC variable (inherited by child blocks)
}

type function struct {
	constantLookup      map[value]int
	f                   *prototype
	previous            *function
	p                   *parser
	block               *block
	jumpPC, lastTarget  int
	freeRegisterCount   int
	activeVariableCount int
	firstLocal          int
	firstLabel          int // Lua 5.4: first label index for this function (like C Lua's fs->firstlabel)
	previousLine        int // Lua 5.4: for relative line info encoding (per-function, like C Lua's FuncState)
	iwthabs             int // instructions without absolute line info
	needClose           bool // Lua 5.4: function has TBC variables (affects RETURN k-bit)
}

func (f *function) OpenFunction(line int) {
	f.f.prototypes = append(f.f.prototypes, prototype{source: f.p.source, maxStackSize: 2, lineDefined: line})
	f.p.function = &function{f: &f.f.prototypes[len(f.f.prototypes)-1], constantLookup: make(map[value]int), previous: f, p: f.p, jumpPC: noJump, firstLocal: len(f.p.activeVariables), firstLabel: len(f.p.activeLabels), previousLine: line}
	f.p.function.EnterBlock(false)
}

func (f *function) CloseFunction() exprDesc {
	e := f.previous.ExpressionToNextRegister(makeExpression(kindRelocatable, f.previous.encodeABx(opClosure, 0, len(f.previous.f.prototypes)-1)))
	f.ReturnNone()
	f.LeaveBlock()
	f.assert(f.block == nil)
	f.finish()
	f.p.function = f.previous
	return e
}

func (f *function) EnterBlock(isLoop bool) {
	// TODO www.lua.org uses a trick here to stack allocate the block, and chain blocks in the stack
	parentTBC := f.block != nil && f.block.insidetbc
	f.block = &block{previous: f.block, firstLabel: len(f.p.activeLabels), firstGoto: len(f.p.pendingGotos), activeVariableCount: f.activeVariableCount, isLoop: isLoop, insidetbc: parentTBC}
	f.assert(f.freeRegisterCount == f.regLevel())
}

func (f *function) undefinedGotoError(g label) {
	if isReserved(g.name) {
		f.semanticError(fmt.Sprintf("<%s> at line %d not inside a loop", g.name, g.line))
	} else {
		f.semanticError(fmt.Sprintf("no visible label '%s' for <goto> at line %d", g.name, g.line))
	}
}

func (f *function) LocalVariable(i int) *localVariable {
	index := f.p.activeVariables[f.firstLocal+i]
	return &f.f.localVariables[index]
}

func (f *function) AdjustLocalVariables(n int) {
	for f.activeVariableCount += n; n != 0; n-- {
		f.LocalVariable(f.activeVariableCount - n).startPC = pc(len(f.f.code))
	}
}

func (f *function) removeLocalVariables(level int) {
	for i := level; i < f.activeVariableCount; i++ {
		f.LocalVariable(i).endPC = pc(len(f.f.code))
	}
	f.p.activeVariables = f.p.activeVariables[:len(f.p.activeVariables)-(f.activeVariableCount-level)]
	f.activeVariableCount = level
}

func (f *function) MakeLocalVariable(name string) {
	r := len(f.f.localVariables)
	f.f.localVariables = append(f.f.localVariables, localVariable{name: name})
	f.p.checkLimit(len(f.p.activeVariables)+1-f.firstLocal, maxLocalVariables, "local variables")
	f.p.activeVariables = append(f.p.activeVariables, r)
}

// markToBeClose marks the current block as having a to-be-closed variable.
// This matches C Lua 5.4's marktobeclosed: only marks the current block,
// plus sets the function-level needClose flag for RETURN k-bit.
func (f *function) markToBeClose() {
	bl := f.block
	bl.hasUpValue = true // ensures OP_CLOSE at block exit
	bl.insidetbc = true
	f.needClose = true // function-level: affects RETURN k-bit
}

// regLevelAt returns the register level at variable scope level nvar.
// CTC variables don't occupy registers, so they are skipped.
func (f *function) regLevelAt(nvar int) int {
	count := 0
	for i := 0; i < nvar; i++ {
		if f.LocalVariable(i).kind != varCTC {
			count++
		}
	}
	return count
}

// regLevel returns the current register level (number of register-occupying variables).
func (f *function) regLevel() int {
	return f.regLevelAt(f.activeVariableCount)
}

// varToReg converts a variable index to its register index by counting
// non-CTC variables before it.
func (f *function) varToReg(vidx int) int {
	reg := 0
	for i := 0; i < vidx; i++ {
		if f.LocalVariable(i).kind != varCTC {
			reg++
		}
	}
	return reg
}

// exp2const checks if an expression is a compile-time constant and returns its value.
func (f *function) exp2const(e exprDesc) (value, bool) {
	if e.hasJumps() {
		return nil, false
	}
	switch e.kind {
	case kindNil:
		return nil, true
	case kindTrue:
		return true, true
	case kindFalse:
		return false, true
	case kindInteger:
		return e.ivalue, true
	case kindNumber:
		return e.value, true
	case kindString:
		return e.strVal, true
	default:
		return nil, false
	}
}

// const2exp converts a compile-time constant value back to an expression.
func const2exp(v value) exprDesc {
	switch v := v.(type) {
	case nil:
		return makeExpression(kindNil, 0)
	case bool:
		if v {
			return makeExpression(kindTrue, 0)
		}
		return makeExpression(kindFalse, 0)
	case int64:
		e := makeExpression(kindInteger, 0)
		e.ivalue = v
		return e
	case float64:
		e := makeExpression(kindNumber, 0)
		e.value = v
		return e
	case string:
		e := makeExpression(kindString, 0)
		e.strVal = v
		return e
	default:
		return makeExpression(kindNil, 0)
	}
}

// isConstantKind returns true if the expression kind is a compile-time constant.
func isConstantKind(k int) bool {
	return k == kindNil || k == kindTrue || k == kindFalse ||
		k == kindInteger || k == kindNumber || k == kindString
}

// checkReadOnly checks if an expression refers to a read-only variable (<const> or <close>).
func (f *function) checkReadOnly(e exprDesc) {
	// CTC constant expressions carry their variable name for error messages
	if e.ctcName != "" {
		f.semanticError(fmt.Sprintf(
			"attempt to assign to const variable '%s'", e.ctcName))
	}
	switch e.kind {
	case kindLocal:
		lv := f.LocalVariable(e.info)
		if lv.kind != varRegular {
			f.semanticError(fmt.Sprintf(
				"attempt to assign to const variable '%s'", lv.name))
		}
	case kindUpValue:
		uv := f.f.upValues[e.info]
		if uv.kind != varRegular {
			f.semanticError(fmt.Sprintf(
				"attempt to assign to const variable '%s'", uv.name))
		}
	}
}

func (f *function) MakeGoto(name string, line, pc int) {
	f.p.pendingGotos = append(f.p.pendingGotos, label{name: name, line: line, pc: pc, activeVariableCount: f.activeVariableCount})
	f.findLabel(len(f.p.pendingGotos) - 1)
}

func (f *function) MakeLabel(name string, line int) int {
	// Mark current position as a jump target to prevent LOADNIL optimization
	// from merging across labels (bug fix for 5.2 -> 5.3.2)
	f.lastTarget = len(f.f.code)
	f.p.activeLabels = append(f.p.activeLabels, label{name: name, line: line, pc: len(f.f.code), activeVariableCount: f.activeVariableCount})
	return len(f.p.activeLabels) - 1
}

func (f *function) closeGoto(i int, l label) {
	g := f.p.pendingGotos[i]
	if f.assert(g.name == l.name); g.activeVariableCount < l.activeVariableCount {
		f.semanticError(fmt.Sprintf("<goto %s> at line %d jumps into the scope of local '%s'", g.name, g.line, f.LocalVariable(g.activeVariableCount).name))
	}
	f.PatchList(g.pc, l.pc)
	copy(f.p.pendingGotos[i:], f.p.pendingGotos[i+1:])
	f.p.pendingGotos = f.p.pendingGotos[:len(f.p.pendingGotos)-1]
}

func (f *function) findLabel(i int) int {
	g, b := f.p.pendingGotos[i], f.block
	// Lua 5.4: search all labels in the entire function (not just current block)
	for _, l := range f.p.activeLabels[f.firstLabel:] {
		if l.name == g.name {
			if g.activeVariableCount > l.activeVariableCount && (b.hasUpValue || len(f.p.activeLabels) > b.firstLabel) {
				f.p.pendingGotos[i].close = true
			}
			f.closeGoto(i, l)
			return 0
		}
	}
	return 1
}

// findExistingLabel searches for an already-declared label with the given name
// in the current function. Returns a pointer to the label or nil if not found.
// Used by gotoStatement to detect backward jumps (Lua 5.4: C Lua's findlabel).
func (f *function) findExistingLabel(name string) *label {
	for i := f.firstLabel; i < len(f.p.activeLabels); i++ {
		if f.p.activeLabels[i].name == name {
			return &f.p.activeLabels[i]
		}
	}
	return nil
}

func (f *function) CheckRepeatedLabel(name string) {
	// Lua 5.4: check all labels in the entire function (not just current block)
	for _, l := range f.p.activeLabels[f.firstLabel:] {
		if l.name == name {
			f.semanticError(fmt.Sprintf("label '%s' already defined on line %d", name, l.line))
		}
	}
}

func (f *function) FindGotos(label int) bool {
	needClose := false
	for i, l := f.block.firstGoto, f.p.activeLabels[label]; i < len(f.p.pendingGotos); {
		if f.p.pendingGotos[i].name == l.name {
			needClose = needClose || f.p.pendingGotos[i].close
			f.closeGoto(i, l)
		} else {
			i++
		}
	}
	return needClose
}

func (f *function) moveGotosOut(b block) {
	for i := b.firstGoto; i < len(f.p.pendingGotos); i += f.findLabel(i) {
		if f.p.pendingGotos[i].activeVariableCount > b.activeVariableCount {
			if b.hasUpValue {
				f.p.pendingGotos[i].close = true
			}
			f.p.pendingGotos[i].activeVariableCount = b.activeVariableCount
		}
	}
}

func (f *function) LeaveBlock() {
	b := f.block
	hasClose := false
	stklevel := f.regLevelAt(b.activeVariableCount)
	f.removeLocalVariables(b.activeVariableCount)
	f.assert(b.activeVariableCount == f.activeVariableCount)
	if b.isLoop {
		hasClose = f.breakLabel() // close pending breaks
	}
	if !hasClose && b.previous != nil && b.hasUpValue {
		f.EncodeABC(opClose, stklevel, 0, 0)
	}
	f.freeRegisterCount = stklevel
	f.p.activeLabels = f.p.activeLabels[:b.firstLabel]
	f.block = b.previous
	if b.previous != nil { // inner block
		f.moveGotosOut(*b) // update pending gotos to outer block
	} else if b.firstGoto < len(f.p.pendingGotos) { // pending gotos in outer block
		f.undefinedGotoError(f.p.pendingGotos[b.firstGoto])
	}
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func not(b int) int {
	if b == 0 {
		return 1
	}
	return 0
}

func makeExpression(kind, info int) exprDesc {
	return exprDesc{f: noJump, t: noJump, kind: kind, info: info}
}

func (f *function) semanticError(message string) {
	f.p.t = 0 // remove "near to" from final message
	f.p.syntaxError(message)
}

func (f *function) breakLabel() bool {
	needClose := f.FindGotos(f.MakeLabel("break", 0))
	if needClose {
		f.EncodeABC(opClose, f.regLevel(), 0, 0)
	}
	return needClose
}
func (f *function) unreachable()                        { f.assert(false) }
func (f *function) assert(cond bool)                    { f.p.l.assert(cond) }
func (f *function) Instruction(e exprDesc) *instruction { return &f.f.code[e.info] }
func (e exprDesc) hasJumps() bool                       { return e.t != e.f }
func (e exprDesc) isNumeral() bool {
	return (e.kind == kindNumber || e.kind == kindInteger) && e.t == noJump && e.f == noJump
}
func (e exprDesc) isVariable() bool {
	return kindLocal <= e.kind && e.kind <= kindIndexStr
}
func (e exprDesc) hasMultipleReturns() bool { return e.kind == kindCall || e.kind == kindVarArg }

func (f *function) assertEqual(a, b interface{}) {
	if a != b {
		panic(fmt.Sprintf("%v != %v", a, b))
	}
}

const (
	lineInfoAbs = -0x80 // marker for absolute line info in lineInfo
	limLineDiff = 0x80   // max absolute delta that fits in int8
	maxIWthAbs  = 128    // max instructions without absolute line info
)

func (f *function) encode(i instruction) int {
	f.assert(len(f.f.code) == len(f.f.lineInfo))
	f.dischargeJumpPC()
	f.f.code = append(f.f.code, i)
	f.saveLineInfo(f.p.lastLine)
	return len(f.f.code) - 1
}

func (f *function) saveLineInfo(line int) {
	lineDiff := line - f.previousLine
	pc := len(f.f.code) - 1
	if lineDiff < -limLineDiff+1 || lineDiff >= limLineDiff || f.iwthabs >= maxIWthAbs {
		// Need absolute line info entry
		f.f.absLineInfos = append(f.f.absLineInfos, absLineInfo{pc: pc, line: line})
		lineDiff = lineInfoAbs
		f.iwthabs = 1
	} else {
		f.iwthabs++
	}
	f.f.lineInfo = append(f.f.lineInfo, int8(lineDiff))
	f.previousLine = line
}

func (f *function) dropLastInstruction() {
	f.assert(len(f.f.code) == len(f.f.lineInfo))
	// Remove line info for the last instruction (like C Lua's removelastlineinfo)
	lastIdx := len(f.f.lineInfo) - 1
	if f.f.lineInfo[lastIdx] != lineInfoAbs {
		// Relative line info: restore previousLine
		f.previousLine -= int(f.f.lineInfo[lastIdx])
		f.iwthabs--
	} else {
		// Absolute line info: remove the entry
		f.f.absLineInfos = f.f.absLineInfos[:len(f.f.absLineInfos)-1]
		// Force next line info to be absolute
		f.iwthabs = maxIWthAbs + 1
	}
	f.f.code = f.f.code[:len(f.f.code)-1]
	f.f.lineInfo = f.f.lineInfo[:len(f.f.lineInfo)-1]
}

func (f *function) EncodeABC(op opCode, a, b, c int) int {
	f.assert(opMode(op) == iABC)
	f.assert(a <= maxArgA && b <= maxArgB && c <= maxArgC)
	return f.encode(createABCk(op, a, b, c, 0))
}

func (f *function) EncodeABCk(op opCode, a, b, c, k int) int {
	f.assert(opMode(op) == iABC)
	f.assert(a <= maxArgA && b <= maxArgB && c <= maxArgC)
	return f.encode(createABCk(op, a, b, c, k))
}

func (f *function) encodeABx(op opCode, a, bx int) int {
	f.assert(opMode(op) == iABx || opMode(op) == iAsBx)
	f.assert(a <= maxArgA && bx <= maxArgBx)
	return f.encode(createABx(op, a, bx))
}

func (f *function) encodeAsBx(op opCode, a, sbx int) int { return f.encodeABx(op, a, sbx+maxArgSBx) }

func (f *function) encodeExtraArg(a int) int {
	f.assert(a <= maxArgAx)
	return f.encode(createAx(opExtraArg, a))
}

func (f *function) EncodeConstant(r, constant int) int {
	if constant <= maxArgBx {
		return f.encodeABx(opLoadConstant, r, constant)
	}
	// Use opLoadConstantEx (LOADKX) for constants with index > maxArgBx
	// The constant index is stored in the following EXTRAARG instruction
	pc := f.encodeABx(opLoadConstantEx, r, 0)
	f.encodeExtraArg(constant)
	return pc
}

func (f *function) EncodeString(s string) exprDesc {
	e := makeExpression(kindString, 0)
	e.strVal = s
	return e
}

func (f *function) loadNil(from, n int) {
	if len(f.f.code) > f.lastTarget { // no jumps to current position
		if previous := &f.f.code[len(f.f.code)-1]; previous.opCode() == opLoadNil {
			if pf, pl, l := previous.a(), previous.a()+previous.b(), from+n-1; pf <= from && from <= pl+1 || from <= pf && pf <= l+1 { // can connect both
				from, l = min(from, pf), max(l, pl)
				previous.setA(from)
				previous.setB(l - from)
				return
			}
		}
	}
	f.EncodeABC(opLoadNil, from, n-1, 0)
}

func (f *function) encodeJ(op opCode, j int) int {
	f.assert(opMode(op) == isJ)
	return f.encode(createSJ(op, j, 0))
}

func (f *function) Jump() int {
	f.assert(f.isJumpListWalkable(f.jumpPC))
	jumpPC := f.jumpPC
	f.jumpPC = noJump
	return f.Concatenate(f.encodeJ(opJump, noJump), jumpPC)
}

func (f *function) JumpTo(target int)             { f.PatchList(f.Jump(), target) }
func (f *function) ReturnNone() {
	k := 0
	if f.needClose {
		k = 1
	}
	f.EncodeABCk(opReturn0, f.regLevel(), 1, 0, k)
}
func (f *function) SetMultipleReturns(e exprDesc) { f.setReturns(e, MultipleReturns) }

func (f *function) Return(e exprDesc, resultCount int) {
	k := 0
	if f.needClose {
		k = 1
	}
	if e.hasMultipleReturns() {
		if f.SetMultipleReturns(e); e.kind == kindCall && resultCount == 1 && !f.needClose {
			f.Instruction(e).setOpCode(opTailCall)
			f.assert(f.Instruction(e).a() == f.regLevel())
		}
		f.EncodeABCk(opReturn, f.regLevel(), MultipleReturns+1, 0, k)
	} else if resultCount == 1 {
		first := f.ExpressionToAnyRegister(e).info
		f.EncodeABCk(opReturn1, first, 2, 0, k)
	} else {
		_ = f.ExpressionToNextRegister(e)
		f.assert(resultCount == f.freeRegisterCount-f.regLevel())
		f.EncodeABCk(opReturn, f.regLevel(), resultCount+1, 0, k)
	}
}

func (f *function) conditionalJump(op opCode, a, b, c, k int) int {
	f.EncodeABCk(op, a, b, c, k)
	return f.Jump()
}

func (f *function) fixJump(pc, dest int) {
	f.assert(f.isJumpListWalkable(pc))
	f.assert(dest != noJump)
	offset := dest - (pc + 1)
	if abs(offset) > offsetSJ {
		f.p.syntaxError("control structure too long")
	}
	f.assert(f.f.code[pc].opCode() == opJump)
	f.f.code[pc].setSJ(offset)
}

func (f *function) Label() int {
	f.lastTarget = len(f.f.code)
	return f.lastTarget
}

func (f *function) jump(pc int) int {
	f.assert(f.isJumpListWalkable(pc))
	if offset := f.f.code[pc].sJ(); offset != noJump {
		return pc + 1 + offset
	}
	return noJump
}

func (f *function) isJumpListWalkable(list int) bool {
	if list == noJump {
		return true
	}
	if list < 0 || list >= len(f.f.code) {
		return false
	}
	offset := f.f.code[list].sJ()
	return offset == noJump || f.isJumpListWalkable(list+1+offset)
}

func (f *function) jumpControl(pc int) *instruction {
	if pc >= 1 && testTMode(f.f.code[pc-1].opCode()) {
		return &f.f.code[pc-1]
	}
	return &f.f.code[pc]
}

func (f *function) needValue(list int) bool {
	f.assert(f.isJumpListWalkable(list))
	for ; list != noJump; list = f.jump(list) {
		if f.jumpControl(list).opCode() != opTestSet {
			return true
		}
	}
	return false
}

func (f *function) patchTestRegister(node, register int) bool {
	if i := f.jumpControl(node); i.opCode() != opTestSet {
		return false
	} else if register != noRegister && register != i.b() {
		i.setA(register)
	} else {
		*i = createABCk(opTest, i.b(), 0, 0, i.k())
	}
	return true
}

func (f *function) removeValues(list int) {
	f.assert(f.isJumpListWalkable(list))
	for ; list != noJump; list = f.jump(list) {
		_ = f.patchTestRegister(list, noRegister)
	}
}

func (f *function) patchListHelper(list, target, register, defaultTarget int) {
	f.assert(f.isJumpListWalkable(list))
	for list != noJump {
		next := f.jump(list)
		if f.patchTestRegister(list, register) {
			f.fixJump(list, target)
		} else {
			f.fixJump(list, defaultTarget)
		}
		list = next
	}
}

func (f *function) dischargeJumpPC() {
	f.assert(f.isJumpListWalkable(f.jumpPC))
	f.patchListHelper(f.jumpPC, len(f.f.code), noRegister, len(f.f.code))
	f.jumpPC = noJump
}

func (f *function) PatchList(list, target int) {
	if target == len(f.f.code) {
		f.PatchToHere(list)
	} else {
		f.assert(target < len(f.f.code))
		f.patchListHelper(list, target, noRegister, target)
	}
}

// PatchClose is a no-op in 5.4. In 5.3, it patched JMP's A register for closing
// upvalues. In 5.4, JMP has isJ format (no A register), and explicit OP_CLOSE
// instructions are emitted instead.
func (f *function) PatchClose(list, level int) {
	// No-op: callers now emit opClose directly or set close flags on gotos
}

func (f *function) PatchToHere(list int) {
	f.assert(f.isJumpListWalkable(list))
	f.assert(f.isJumpListWalkable(f.jumpPC))
	f.Label()
	f.jumpPC = f.Concatenate(f.jumpPC, list)
	f.assert(f.isJumpListWalkable(f.jumpPC))
}

func (f *function) Concatenate(l1, l2 int) int {
	f.assert(f.isJumpListWalkable(l1))
	switch {
	case l2 == noJump:
	case l1 == noJump:
		return l2
	default:
		list := l1
		for next := f.jump(list); next != noJump; list, next = next, f.jump(next) {
		}
		f.fixJump(list, l2)
	}
	return l1
}

func (f *function) addConstant(k, v value) int {
	if index, ok := f.constantLookup[k]; ok && f.f.constants[index] == v {
		return index
	}
	index := len(f.f.constants)
	f.constantLookup[k] = index
	f.f.constants = append(f.f.constants, v)
	return index
}

func (f *function) NumberConstant(n float64) int {
	if n == 0.0 || math.IsNaN(n) {
		return f.addConstant(math.Float64bits(n), n)
	}
	return f.addConstant(n, n)
}

// IntegerConstant adds an integer constant to the constant table (Lua 5.3)
func (f *function) IntegerConstant(n int64) int {
	// Use a distinct key type to differentiate int64 from float64
	type intKey struct{ v int64 }
	return f.addConstant(intKey{n}, n)
}

func (f *function) CheckStack(n int) {
	if n += f.freeRegisterCount; n >= maxStack {
		f.p.syntaxError("function or expression too complex")
	} else if n > f.f.maxStackSize {
		f.f.maxStackSize = n
	}
}

func (f *function) ReserveRegisters(n int) {
	f.CheckStack(n)
	f.freeRegisterCount += n
}

func (f *function) freeRegister(r int) {
	if r >= f.regLevel() {
		f.freeRegisterCount--
		f.assertEqual(r, f.freeRegisterCount)
	}
}

func (f *function) freeExpression(e exprDesc) {
	if e.kind == kindNonRelocatable {
		f.freeRegister(e.info)
	}
}

// freeExpressions frees two expressions in the correct LIFO order (higher register first).
func (f *function) freeExpressions(e1, e2 exprDesc) {
	r1 := -1
	r2 := -1
	if e1.kind == kindNonRelocatable {
		r1 = e1.info
	}
	if e2.kind == kindNonRelocatable {
		r2 = e2.info
	}
	if r1 > r2 {
		f.freeRegister(r1)
		if r2 >= 0 {
			f.freeRegister(r2)
		}
	} else {
		if r2 >= 0 {
			f.freeRegister(r2)
		}
		if r1 >= 0 {
			f.freeRegister(r1)
		}
	}
}

func (f *function) stringConstant(s string) int { return f.addConstant(s, s) }
func (f *function) booleanConstant(b bool) int  { return f.addConstant(b, b) }
func (f *function) nilConstant() int            { return f.addConstant(f, nil) }

func (f *function) setReturns(e exprDesc, resultCount int) {
	if e.kind == kindCall {
		f.Instruction(e).setC(resultCount + 1)
	} else if e.kind == kindVarArg {
		f.Instruction(e).setC(resultCount + 1) // 5.4: VARARG uses C field
		f.Instruction(e).setA(f.freeRegisterCount)
		f.ReserveRegisters(1)
	}
}

func (f *function) SetReturn(e exprDesc) exprDesc {
	if e.kind == kindCall {
		e.kind, e.info = kindNonRelocatable, f.Instruction(e).a()
	} else if e.kind == kindVarArg {
		f.Instruction(e).setC(2) // 5.4: VARARG uses C field
		e.kind = kindRelocatable
	}
	return e
}

func (f *function) DischargeVariables(e exprDesc) exprDesc {
	switch e.kind {
	case kindLocal:
		e.info = f.varToReg(e.info) // convert variable index to register
		e.kind = kindNonRelocatable
	case kindUpValue:
		e.kind, e.info = kindRelocatable, f.EncodeABC(opGetUpValue, 0, e.info, 0)
	case kindString:
		e.kind, e.info = kindConstant, f.stringConstant(e.strVal)
	case kindIndexUp:
		e.kind, e.info = kindRelocatable, f.EncodeABC(opGetTableUp, 0, e.table, e.index)
	case kindIndexInt:
		f.freeRegister(e.table)
		e.kind, e.info = kindRelocatable, f.EncodeABC(opGetI, 0, e.table, e.index)
	case kindIndexStr:
		f.freeRegister(e.table)
		e.kind, e.info = kindRelocatable, f.EncodeABC(opGetField, 0, e.table, e.index)
	case kindIndexed:
		// Free in LIFO order (higher register first), like C Lua's freeregs()
		if e.table > e.index {
			f.freeRegister(e.table)
			f.freeRegister(e.index)
		} else {
			f.freeRegister(e.index)
			f.freeRegister(e.table)
		}
		e.kind, e.info = kindRelocatable, f.EncodeABC(opGetTable, 0, e.table, e.index)
	case kindVarArg, kindCall:
		e = f.SetReturn(e)
	}
	return e
}

func (f *function) dischargeToRegister(e exprDesc, r int) exprDesc {
	switch e = f.DischargeVariables(e); e.kind {
	case kindNil:
		f.loadNil(r, 1)
	case kindFalse:
		f.EncodeABC(opLoadFalse, r, 0, 0)
	case kindTrue:
		f.EncodeABC(opLoadTrue, r, 0, 0)
	case kindConstant:
		f.EncodeConstant(r, e.info)
	case kindNumber:
		if fi, ok := floatToInteger(e.value); ok && fi >= -maxArgSBx && fi <= maxArgSBx+1 && !(fi == 0 && math.Signbit(e.value)) {
			f.encodeAsBx(opLoadF, r, int(fi))
		} else {
			f.EncodeConstant(r, f.NumberConstant(e.value))
		}
	case kindInteger:
		if e.ivalue >= -maxArgSBx && e.ivalue <= maxArgSBx+1 {
			f.encodeAsBx(opLoadI, r, int(e.ivalue))
		} else {
			f.EncodeConstant(r, f.IntegerConstant(e.ivalue))
		}
	case kindString:
		f.EncodeConstant(r, f.stringConstant(e.strVal))
	case kindRelocatable:
		f.Instruction(e).setA(r)
	case kindNonRelocatable:
		if r != e.info {
			f.EncodeABC(opMove, r, e.info, 0)
		}
	default:
		f.assert(e.kind == kindVoid || e.kind == kindJump)
		return e
	}
	e.kind, e.info = kindNonRelocatable, r
	return e
}

func (f *function) dischargeToAnyRegister(e exprDesc) exprDesc {
	if e.kind != kindNonRelocatable {
		f.ReserveRegisters(1)
		e = f.dischargeToRegister(e, f.freeRegisterCount-1)
	}
	return e
}

func (f *function) encodeLabel(a, b, jump int) int {
	f.Label()
	// Lua 5.4: opLoadFalseSkip produces false and skips next,
	// opLoadTrue produces true. Used for boolean coercion.
	if b != 0 {
		return f.EncodeABC(opLoadTrue, a, 0, 0)
	}
	if jump != 0 {
		return f.EncodeABC(opLoadFalseSkip, a, 0, 0)
	}
	return f.EncodeABC(opLoadFalse, a, 0, 0)
}

func (f *function) expressionToRegister(e exprDesc, r int) exprDesc {
	if e = f.dischargeToRegister(e, r); e.kind == kindJump {
		e.t = f.Concatenate(e.t, e.info)
	}
	if e.hasJumps() {
		loadFalse, loadTrue := noJump, noJump
		if f.needValue(e.t) || f.needValue(e.f) {
			jump := noJump
			if e.kind != kindJump {
				jump = f.Jump()
			}
			loadFalse, loadTrue = f.encodeLabel(r, 0, 1), f.encodeLabel(r, 1, 0)
			f.PatchToHere(jump)
		}
		end := f.Label()
		f.patchListHelper(e.f, end, r, loadFalse)
		f.patchListHelper(e.t, end, r, loadTrue)
	}
	e.f, e.t, e.info, e.kind = noJump, noJump, r, kindNonRelocatable
	return e
}

func (f *function) ExpressionToNextRegister(e exprDesc) exprDesc {
	e = f.DischargeVariables(e)
	f.freeExpression(e)
	f.ReserveRegisters(1)
	return f.expressionToRegister(e, f.freeRegisterCount-1)
}

func (f *function) ExpressionToAnyRegister(e exprDesc) exprDesc {
	if e = f.DischargeVariables(e); e.kind == kindNonRelocatable {
		if !e.hasJumps() {
			return e
		}
		if e.info >= f.regLevel() {
			return f.expressionToRegister(e, e.info)
		}
	}
	return f.ExpressionToNextRegister(e)
}

func (f *function) ExpressionToAnyRegisterOrUpValue(e exprDesc) exprDesc {
	if e.kind != kindUpValue || e.hasJumps() {
		e = f.ExpressionToAnyRegister(e)
	}
	return e
}

func (f *function) ExpressionToValue(e exprDesc) exprDesc {
	if e.hasJumps() {
		return f.ExpressionToAnyRegister(e)
	}
	return f.DischargeVariables(e)
}


// exp2K tries to convert expression to a constant index in range.
// Returns (constant index, true) if successful, (0, false) otherwise.
func (f *function) exp2K(e exprDesc) (int, bool) {
	if e.hasJumps() {
		return 0, false
	}
	var info int
	switch e.kind {
	case kindTrue:
		info = f.booleanConstant(true)
	case kindFalse:
		info = f.booleanConstant(false)
	case kindNil:
		info = f.nilConstant()
	case kindInteger:
		info = f.IntegerConstant(e.ivalue)
	case kindNumber:
		info = f.NumberConstant(e.value)
	case kindString:
		info = f.stringConstant(e.strVal)
	case kindConstant:
		info = e.info
	default:
		return 0, false
	}
	if info > maxArgB {
		return 0, false
	}
	return info, true
}

// codeABRK emits an instruction with the value in C as either a register (k=0)
// or constant index (k=1).
func (f *function) codeABRK(op opCode, a, b int, ec exprDesc) {
	if info, ok := f.exp2K(ec); ok {
		f.EncodeABCk(op, a, b, info, 1)
	} else {
		ec = f.ExpressionToAnyRegister(ec)
		f.EncodeABCk(op, a, b, ec.info, 0)
	}
}

func (f *function) StoreVariable(v, e exprDesc) {
	switch v.kind {
	case kindLocal:
		f.freeExpression(e)
		f.expressionToRegister(e, f.varToReg(v.info))
		return
	case kindUpValue:
		e = f.ExpressionToAnyRegister(e)
		f.EncodeABC(opSetUpValue, e.info, v.info, 0)
	case kindIndexUp:
		f.codeABRK(opSetTableUp, v.table, v.index, e)
	case kindIndexInt:
		f.codeABRK(opSetI, v.table, v.index, e)
	case kindIndexStr:
		f.codeABRK(opSetField, v.table, v.index, e)
	case kindIndexed:
		f.codeABRK(opSetTable, v.table, v.index, e)
	default:
		f.unreachable()
	}
	f.freeExpression(e)
}

func (f *function) Self(e, key exprDesc) exprDesc {
	e = f.ExpressionToAnyRegister(e)
	r := e.info
	f.freeExpression(e)
	result := exprDesc{info: f.freeRegisterCount, kind: kindNonRelocatable, t: noJump, f: noJump}
	f.ReserveRegisters(2) // function and 'self' produced by opSelf
	f.codeABRK(opSelf, result.info, r, key)
	f.freeExpression(key)
	return result
}

func (f *function) invertJump(pc int) {
	i := f.jumpControl(pc)
	f.p.l.assert(testTMode(i.opCode()) && i.opCode() != opTestSet && i.opCode() != opTest)
	i.setK(not(i.k()))
}

func (f *function) jumpOnCondition(e exprDesc, cond int) int {
	if e.kind == kindRelocatable {
		if i := f.Instruction(e); i.opCode() == opNot {
			f.dropLastInstruction() // remove previous opNot
			return f.conditionalJump(opTest, i.b(), 0, 0, not(cond))
		}
	}
	e = f.dischargeToAnyRegister(e)
	f.freeExpression(e)
	return f.conditionalJump(opTestSet, noRegister, e.info, 0, cond)
}

func (f *function) GoIfTrue(e exprDesc) exprDesc {
	pc := noJump
	switch e = f.DischargeVariables(e); e.kind {
	case kindJump:
		f.invertJump(e.info)
		pc = e.info
	case kindConstant, kindNumber, kindInteger, kindString, kindTrue:
	default:
		pc = f.jumpOnCondition(e, 0)
	}
	e.f = f.Concatenate(e.f, pc)
	f.PatchToHere(e.t)
	e.t = noJump
	return e
}

func (f *function) GoIfFalse(e exprDesc) exprDesc {
	pc := noJump
	switch e = f.DischargeVariables(e); e.kind {
	case kindJump:
		pc = e.info
	case kindNil, kindFalse:
	default:
		pc = f.jumpOnCondition(e, 1)
	}
	e.t = f.Concatenate(e.t, pc)
	f.PatchToHere(e.f)
	e.f = noJump
	return e
}

func (f *function) encodeNot(e exprDesc) exprDesc {
	switch e = f.DischargeVariables(e); e.kind {
	case kindNil, kindFalse:
		e.kind = kindTrue
	case kindConstant, kindNumber, kindInteger, kindString, kindTrue:
		e.kind = kindFalse
	case kindJump:
		f.invertJump(e.info)
	case kindRelocatable, kindNonRelocatable:
		e = f.dischargeToAnyRegister(e)
		f.freeExpression(e)
		e.info, e.kind = f.EncodeABC(opNot, 0, e.info, 0), kindRelocatable
	default:
		f.unreachable()
	}
	e.f, e.t = e.t, e.f
	f.removeValues(e.f)
	f.removeValues(e.t)
	return e
}

// isKstr checks if expression is a string constant that fits in B.
func (f *function) isKstr(e exprDesc) bool {
	if e.kind == kindString {
		return true
	}
	return e.kind == kindConstant && !e.hasJumps() && e.info <= maxArgB &&
		isString(f.f.constants[e.info])
}

// isCint checks if expression is a non-negative integer that fits in C.
func isCint(e exprDesc) bool {
	return e.kind == kindInteger && !e.hasJumps() &&
		e.ivalue >= 0 && e.ivalue <= int64(maxArgC)
}

func isString(v value) bool {
	_, ok := v.(string)
	return ok
}

func (f *function) Indexed(t, k exprDesc) exprDesc {
	f.assert(!t.hasJumps())
	// Convert kindString to kindConstant for indexing
	if k.kind == kindString {
		k = makeExpression(kindConstant, f.stringConstant(k.strVal))
	}
	if t.kind == kindUpValue && !f.isKstr(k) {
		// Upvalue indexed by non-string-constant: put upvalue in a register
		t = f.ExpressionToAnyRegister(t)
	}
	if t.kind == kindUpValue {
		f.assert(f.isKstr(k))
		r := makeExpression(kindIndexUp, 0)
		r.table = t.info     // upvalue index
		r.index = k.info     // string constant index
		return r
	}
	// table is in a register
	tableReg := t.info
	if t.kind == kindLocal {
		tableReg = t.info
	}
	if f.isKstr(k) {
		r := makeExpression(kindIndexStr, 0)
		r.table = tableReg
		r.index = k.info // string constant index
		return r
	}
	if isCint(k) {
		r := makeExpression(kindIndexInt, 0)
		r.table = tableReg
		r.index = int(k.ivalue) // integer key
		return r
	}
	// General case: both in registers
	k = f.ExpressionToAnyRegister(k)
	r := makeExpression(kindIndexed, 0)
	r.table = tableReg
	r.index = k.info // register index
	return r
}

func foldConstants(op opCode, e1, e2 exprDesc) (exprDesc, bool) {
	if !e1.isNumeral() || !e2.isNumeral() {
		return e1, false
	}
	// Handle integer arithmetic and bitwise operations directly in int64 space
	if e1.kind == kindInteger && e2.kind == kindInteger && op != opDiv && op != opPow {
		i1, i2 := e1.ivalue, e2.ivalue
		var result int64
		switch op {
		case opAdd:
			result = i1 + i2
		case opSub:
			result = i1 - i2
		case opMul:
			result = i1 * i2
		case opIDiv:
			if i2 == 0 {
				return e1, false
			}
			result = intIDiv(i1, i2)
		case opMod:
			if i2 == 0 {
				return e1, false
			}
			result = i1 % i2
			// Lua mod: result has same sign as divisor
			if result != 0 && (result^i2) < 0 {
				result += i2
			}
		case opBAnd:
			result = i1 & i2
		case opBOr:
			result = i1 | i2
		case opBXor:
			result = i1 ^ i2
		case opShl:
			result = intShiftLeft(i1, i2)
		case opShr:
			result = intShiftLeft(i1, -i2)
		case opUnaryMinus:
			// Like C Lua: don't fold -MinInt64 (overflow), let VM handle it
			if i1 == math.MinInt64 {
				return e1, false
			}
			result = -i1
		case opBNot:
			result = ^i1
		default:
			return e1, false
		}
		e1.kind = kindInteger
		e1.ivalue = result
		return e1, true
	}

	// Bitwise and idiv require integers - don't fold with float operands
	switch op {
	case opIDiv, opBAnd, opBOr, opBXor, opShl, opShr, opBNot:
		return e1, false
	}

	// Float arithmetic
	var v1, v2 float64
	if e1.kind == kindInteger {
		v1 = float64(e1.ivalue)
	} else {
		v1 = e1.value
	}
	if e2.kind == kindInteger {
		v2 = float64(e2.ivalue)
	} else {
		v2 = e2.value
	}

	// Check for division by zero
	switch op {
	case opDiv, opMod:
		if v2 == 0.0 {
			return e1, false
		}
	}

	var arithOp Operator
	switch op {
	case opAdd:
		arithOp = OpAdd
	case opSub:
		arithOp = OpSub
	case opMul:
		arithOp = OpMul
	case opMod:
		arithOp = OpMod
	case opPow:
		arithOp = OpPow
	case opDiv:
		arithOp = OpDiv
	case opUnaryMinus:
		arithOp = OpUnaryMinus
	default:
		return e1, false
	}

	result := arith(arithOp, v1, v2)
	e1.kind = kindNumber
	e1.value = result
	return e1, true
}

// binopr2TM maps a binary opcode to its tag method.
func binopr2TM(op int) tm {
	// ORDER: oprAdd..oprShr maps to tmAdd..tmShr
	return tm(op-oprAdd) + tmAdd
}

// encodeBinaryOp emits a binary arithmetic opcode followed by MMBIN for metamethods.
func (f *function) encodeBinaryOp(op opCode, e1, e2 exprDesc, line int) exprDesc {
	e2 = f.ExpressionToAnyRegister(e2)
	e1 = f.ExpressionToAnyRegister(e1)
	o1, o2 := e1.info, e2.info
	f.freeExpressions(e1, e2)
	e1.info = f.EncodeABC(op, 0, o1, o2)
	e1.kind = kindRelocatable
	f.FixLine(line)
	// Emit MMBIN for metamethod fallback
	event := binopr2TM(int(op-opAdd) + oprAdd)
	f.EncodeABCk(opMMBin, o1, o2, int(event), 0)
	f.FixLine(line)
	return e1
}

// encodeUnaryOp emits a unary opcode (no MMBIN needed).
func (f *function) encodeUnaryOp(op opCode, e exprDesc, line int) exprDesc {
	e = f.ExpressionToAnyRegister(e)
	r := e.info
	f.freeExpression(e)
	e.info = f.EncodeABC(op, 0, r, 0)
	e.kind = kindRelocatable
	f.FixLine(line)
	return e
}

func (f *function) encodeArithmetic(op opCode, e1, e2 exprDesc, line int) exprDesc {
	if e, folded := foldConstants(op, e1, e2); folded {
		return e
	}
	if op == opUnaryMinus || op == opLength || op == opBNot {
		return f.encodeUnaryOp(op, e1, line)
	}
	return f.encodeBinaryOp(op, e1, e2, line)
}

func (f *function) Prefix(op int, e exprDesc, line int) exprDesc {
	e = f.DischargeVariables(e)
	switch op {
	case oprMinus, oprBNot:
		if e, folded := foldConstants(opCode(op-oprMinus)+opUnaryMinus, e, makeExpression(kindInteger, 0)); folded {
			return e
		}
		return f.encodeUnaryOp(opCode(op-oprMinus)+opUnaryMinus, e, line)
	case oprNot:
		return f.encodeNot(e)
	case oprLength:
		return f.encodeUnaryOp(opLength, e, line)
	}
	panic("unreachable")
}

func (f *function) Infix(op int, e exprDesc) exprDesc {
	e = f.DischargeVariables(e)
	switch op {
	case oprAnd:
		e = f.GoIfTrue(e)
	case oprOr:
		e = f.GoIfFalse(e)
	case oprConcat:
		e = f.ExpressionToNextRegister(e)
	case oprAdd, oprSub, oprMul, oprDiv, oprMod, oprPow, oprIDiv,
		oprBAnd, oprBOr, oprBXor, oprShl, oprShr:
		if !e.isNumeral() {
			e = f.ExpressionToAnyRegister(e)
		}
	case oprEq, oprNE:
		if !e.isNumeral() {
			e = f.ExpressionToAnyRegister(e)
		}
	case oprLT, oprLE, oprGT, oprGE:
		if !e.isNumeral() {
			e = f.ExpressionToAnyRegister(e)
		}
	default:
		e = f.ExpressionToAnyRegister(e)
	}
	return e
}

func (f *function) encodeComparison(op opCode, cond int, e1, e2 exprDesc) exprDesc {
	e1 = f.ExpressionToAnyRegister(e1)
	e2 = f.ExpressionToAnyRegister(e2)
	o1, o2 := e1.info, e2.info
	f.freeExpressions(e1, e2)
	if cond == 0 && op != opEqual {
		o1, o2, cond = o2, o1, 1
	}
	// 5.4: k-bit for condition instead of A register
	e1.info = f.conditionalJump(op, o1, o2, 0, cond)
	e1.kind = kindJump
	return e1
}

func (f *function) Postfix(op int, e1, e2 exprDesc, line int) exprDesc {
	e2 = f.DischargeVariables(e2)
	// Try constant folding for foldable operations
	if isFoldable(op) {
		if e, folded := foldConstants(opCode(op-oprAdd)+opAdd, e1, e2); folded {
			return e
		}
	}
	switch op {
	case oprAnd:
		f.assert(e1.t == noJump)
		e2.f = f.Concatenate(e2.f, e1.f)
		return e2
	case oprOr:
		f.assert(e1.f == noJump)
		e2.t = f.Concatenate(e2.t, e1.t)
		return e2
	case oprConcat:
		e2 = f.ExpressionToNextRegister(e2)
		f.codeConcat(e1, e2, line)
		return e1
	case oprAdd, oprSub, oprMul, oprMod, oprPow, oprDiv, oprIDiv:
		return f.encodeBinaryOp(opCode(op-oprAdd)+opAdd, e1, e2, line)
	case oprBAnd, oprBOr, oprBXor, oprShl, oprShr:
		return f.encodeBinaryOp(opCode(op-oprBAnd)+opBAnd, e1, e2, line)
	case oprEq, oprLT, oprLE:
		return f.encodeComparison(opCode(op-oprEq)+opEqual, 1, e1, e2)
	case oprNE:
		return f.encodeComparison(opEqual, 0, e1, e2)
	case oprGT:
		// (a > b) => (b < a)
		return f.encodeComparison(opLessThan, 1, e2, e1)
	case oprGE:
		// (a >= b) => (b <= a)
		return f.encodeComparison(opLessOrEqual, 1, e2, e1)
	}
	panic("unreachable")
}

func isFoldable(op int) bool {
	return op >= oprAdd && op <= oprShr
}

// codeConcat implements 5.4 CONCAT format: CONCAT A B — R[A] := R[A].. ... ..R[A+B-1]
// e1 is not modified; it stays as NonRelocatable at its register.
func (f *function) codeConcat(e1 exprDesc, e2 exprDesc, line int) {
	// Check if the previous instruction is a CONCAT we can extend
	ie2 := &f.f.code[len(f.f.code)-1]
	if ie2.opCode() == opConcat {
		n := ie2.b()
		f.assert(e1.info+1 == ie2.a())
		f.freeExpression(e2)
		ie2.setA(e1.info)
		ie2.setB(n + 1)
	} else {
		// New CONCAT with 2 elements
		f.EncodeABC(opConcat, e1.info, 2, 0)
		f.freeExpression(e2)
		f.FixLine(line)
	}
}

func (f *function) FixLine(line int) {
	// Like C Lua: removelastlineinfo + savelineinfo
	// First, undo the last lineinfo entry
	lastIdx := len(f.f.lineInfo) - 1
	if f.f.lineInfo[lastIdx] != lineInfoAbs {
		f.previousLine -= int(f.f.lineInfo[lastIdx])
		f.iwthabs--
	} else {
		f.f.absLineInfos = f.f.absLineInfos[:len(f.f.absLineInfos)-1]
		f.iwthabs = maxIWthAbs + 1
	}
	f.f.lineInfo = f.f.lineInfo[:lastIdx]
	// Then save the new line info
	f.saveLineInfo(line)
}

func (f *function) setList(base, offset, storeCount int) {
	// In 5.4, SETLIST A B C k: R[A][C+i] := R[A+i], 1 <= i <= B
	// C = offset (number of items already stored before this batch).
	// B = storeCount (0 means store up to top).
	if storeCount == MultipleReturns {
		storeCount = 0 // B=0 means store up to top
	} else {
		f.assert(storeCount != 0 && storeCount <= listItemsPerFlush)
	}
	if offset <= maxArgC {
		f.EncodeABCk(opSetList, base, storeCount, offset, 0)
	} else {
		extra := offset / (maxArgC + 1)
		rc := offset % (maxArgC + 1)
		f.EncodeABCk(opSetList, base, storeCount, rc, 1)
		f.encodeExtraArg(extra)
	}
	f.freeRegisterCount = base + 1
}

func (f *function) CheckConflict(t *assignmentTarget, e exprDesc) {
	extra, conflict := f.freeRegisterCount, false
	for ; t != nil; t = t.previous {
		switch t.kind {
		case kindIndexed, kindIndexInt, kindIndexStr:
			// These use a table register
			if e.kind == kindLocal && t.table == e.info {
				conflict = true
				t.table = extra
			}
			if t.kind == kindIndexed && e.kind == kindLocal && t.index == e.info {
				conflict = true
				t.index = extra
			}
		case kindIndexUp:
			// Upvalue table + constant key — no register conflict possible
		}
	}
	if conflict {
		if e.kind == kindLocal {
			f.EncodeABC(opMove, extra, e.info, 0)
		} else {
			f.EncodeABC(opGetUpValue, extra, e.info, 0)
		}
		f.ReserveRegisters(1)
	}
}

func (f *function) AdjustAssignment(variableCount, expressionCount int, e exprDesc) {
	if extra := variableCount - expressionCount; e.hasMultipleReturns() {
		if extra++; extra < 0 {
			extra = 0
		}
		if f.setReturns(e, extra); extra > 1 {
			f.ReserveRegisters(extra - 1)
		}
	} else {
		if expressionCount > 0 {
			_ = f.ExpressionToNextRegister(e)
		}
		if extra > 0 {
			r := f.freeRegisterCount
			f.ReserveRegisters(extra)
			f.loadNil(r, extra)
		}
	}
}

func (f *function) makeUpValue(name string, e exprDesc) int {
	f.p.checkLimit(len(f.f.upValues)+1, maxUpValue, "upvalues")
	// For kindLocal, convert variable index to register index for the upvalue
	idx := e.info
	if e.kind == kindLocal && f.previous != nil {
		idx = f.previous.varToReg(e.info)
	}
	uv := upValueDesc{name: name, isLocal: e.kind == kindLocal, index: idx}
	// Propagate kind from local variable or parent upvalue
	if e.kind == kindLocal && f.previous != nil {
		uv.kind = f.previous.LocalVariable(e.info).kind
	} else if e.kind == kindUpValue && f.previous != nil {
		uv.kind = f.previous.f.upValues[e.info].kind
	}
	f.f.upValues = append(f.f.upValues, uv)
	return len(f.f.upValues) - 1
}

func singleVariableHelper(f *function, name string, base bool) (e exprDesc, found bool) {
	owningBlock := func(b *block, level int) *block {
		for b.activeVariableCount > level {
			b = b.previous
		}
		return b
	}
	find := func() (int, bool) {
		for i := f.activeVariableCount - 1; i >= 0; i-- {
			if name == f.LocalVariable(i).name {
				return i, true
			}
		}
		return 0, false
	}
	findUpValue := func() (int, bool) {
		for i, u := range f.f.upValues {
			if u.name == name {
				return i, true
			}
		}
		return 0, false
	}
	if f == nil {
		return
	}
	var v int
	if v, found = find(); found {
		lv := f.LocalVariable(v)
		if lv.kind == varCTC {
			// Compile-time constant: return stored value with variable name
			e = const2exp(lv.val)
			e.ctcName = lv.name
			return e, true
		}
		if e = makeExpression(kindLocal, v); !base {
			owningBlock(f.block, v).hasUpValue = true
		}
		return
	}
	if v, found = findUpValue(); found {
		return makeExpression(kindUpValue, v), true
	}
	if e, found = singleVariableHelper(f.previous, name, false); !found {
		return
	}
	// If the resolved expression is a constant (from a CTC variable in an outer scope),
	// return it directly without creating an upvalue.
	if isConstantKind(e.kind) {
		return e, true
	}
	return makeExpression(kindUpValue, f.makeUpValue(name, e)), true
}

func (f *function) SingleVariable(name string) (e exprDesc) {
	var found bool
	if e, found = singleVariableHelper(f, name, true); !found {
		e, found = singleVariableHelper(f, "_ENV", true)
		f.assert(found && (e.kind == kindLocal || e.kind == kindUpValue))
		e = f.Indexed(e, f.EncodeString(name))
	}
	return
}

func (f *function) OpenConstructor() (pc int, t exprDesc) {
	pc = f.EncodeABC(opNewTable, 0, 0, 0)
	f.encodeExtraArg(0) // placeholder for array size extra, filled by CloseConstructor
	t = f.ExpressionToNextRegister(makeExpression(kindRelocatable, pc))
	return
}

func (f *function) FlushFieldToConstructor(tableRegister, freeRegisterCount int, k exprDesc, v func() exprDesc) {
	// Convert string key to constant
	if k.kind == kindString {
		k = makeExpression(kindConstant, f.stringConstant(k.strVal))
	}
	if f.isKstr(k) {
		// Use SETFIELD for string constant keys
		kIdx := k.info
		val := v()
		f.codeABRK(opSetField, tableRegister, kIdx, val)
	} else {
		// General case: use SETTABLE
		k = f.ExpressionToAnyRegister(k)
		kIdx := k.info
		val := v()
		f.codeABRK(opSetTable, tableRegister, kIdx, val)
	}
	f.freeRegisterCount = freeRegisterCount
}

func (f *function) FlushToConstructor(tableRegister, pending, arrayCount int, e exprDesc) int {
	f.ExpressionToNextRegister(e)
	if pending == listItemsPerFlush {
		f.setList(tableRegister, arrayCount-listItemsPerFlush, listItemsPerFlush)
		pending = 0
	}
	return pending
}

// ceilLog2 computes ceil(log2(x)) for x > 0.
func ceilLog2(x int) int {
	l := 0
	x--
	for x >= (1 << l) {
		l++
	}
	return l
}

func (f *function) CloseConstructor(pc, tableRegister, pending, arrayCount, hashCount int, e exprDesc) {
	if pending != 0 {
		if e.hasMultipleReturns() {
			f.SetMultipleReturns(e)
			f.setList(tableRegister, arrayCount-pending, MultipleReturns)
			arrayCount--
		} else {
			if e.kind != kindVoid {
				f.ExpressionToNextRegister(e)
			}
			f.setList(tableRegister, arrayCount-pending, pending)
		}
	}
	// 5.4: NEWTABLE A B C k, followed by EXTRAARG
	// B = hash size (encoded as ceilLog2(n) + 1, or 0)
	// C = lower bits of array size
	// k = 1 if extra argument holds higher bits
	rb := 0
	if hashCount > 0 {
		rb = ceilLog2(hashCount) + 1
	}
	extra := arrayCount / (maxArgC + 1)
	rc := arrayCount % (maxArgC + 1)
	k := 0
	if extra > 0 {
		k = 1
	}
	f.f.code[pc] = createABCk(opNewTable, f.f.code[pc].a(), rb, rc, k)
	f.f.code[pc+1] = createAx(opExtraArg, extra)
}

func (f *function) OpenForBody(base, n int, isNumeric bool) (prep int) {
	if isNumeric {
		prep = f.encodeABx(opForPrep, base, 0)
	} else {
		prep = f.encodeABx(opTForPrep, base, 0)
	}
	f.EnterBlock(false)
	f.AdjustLocalVariables(n)
	f.ReserveRegisters(n)
	return
}

// fixForJump patches a for-loop jump (FORPREP→body or FORLOOP→body).
// In 5.4, FORPREP/FORLOOP/TFORPREP/TFORLOOP use ABx with unsigned offset.
func (f *function) fixForJump(pc, dest int, back bool) {
	offset := dest - (pc + 1)
	if back {
		offset = -offset
	}
	if offset > maxArgBx {
		f.p.syntaxError("control structure too long")
	}
	f.f.code[pc].setBx(offset)
}

func (f *function) CloseForBody(prep, base, line, n int, isNumeric bool) {
	f.LeaveBlock()
	f.fixForJump(prep, f.Label(), false) // FORPREP/TFORPREP jumps forward to here
	var endFor int
	if isNumeric {
		endFor = f.encodeABx(opForLoop, base, 0)
	} else {
		f.EncodeABC(opTForCall, base, 0, n)
		f.FixLine(line)
		endFor = f.encodeABx(opTForLoop, base+2, 0)
	}
	f.fixForJump(endFor, prep+1, true) // FORLOOP/TFORLOOP jumps back
	f.FixLine(line)
}

func (f *function) OpenMainFunction() {
	f.EnterBlock(false)
	f.makeUpValue("_ENV", makeExpression(kindLocal, 0))
	f.f.isVarArg = true
	f.EncodeABC(opVarArgPrep, 0, 0, 0)
}

func (f *function) CloseMainFunction() *function {
	f.ReturnNone()
	f.LeaveBlock()
	f.assert(f.block == nil)
	f.finish()
	return f.previous
}

// finish does a final pass over the code, converting RETURN0/RETURN1
// to RETURN when needed (vararg functions need parameter count in C).
func (f *function) finish() {
	for i := range f.f.code {
		pc := &f.f.code[i]
		switch pc.opCode() {
		case opReturn0, opReturn1:
			if f.f.isVarArg {
				pc.setOpCode(opReturn)
				pc.setC(f.f.parameterCount + 1)
			}
		case opReturn:
			if f.f.isVarArg {
				pc.setC(f.f.parameterCount + 1)
			}
		}
	}
}
