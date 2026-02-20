package lua

import "fmt"

type opCode uint

// Instruction formats (Lua 5.4)
const (
	iABC int = iota
	iABx
	iAsBx
	iAx
	isJ
)

// Lua 5.4 opcodes — ORDER OP (must match lopcodes.h)
const (
	opMove opCode = iota
	opLoadI
	opLoadF
	opLoadConstant
	opLoadConstantEx
	opLoadFalse
	opLoadFalseSkip
	opLoadTrue
	opLoadNil
	opGetUpValue
	opSetUpValue
	opGetTableUp
	opGetTable
	opGetI
	opGetField
	opSetTableUp
	opSetTable
	opSetI
	opSetField
	opNewTable
	opSelf
	opAddI
	opAddK
	opSubK
	opMulK
	opModK
	opPowK
	opDivK
	opIDivK
	opBAndK
	opBOrK
	opBXorK
	opShrI
	opShlI
	opAdd
	opSub
	opMul
	opMod
	opPow
	opDiv
	opIDiv
	opBAnd
	opBOr
	opBXor
	opShl
	opShr
	opMMBin
	opMMBinI
	opMMBinK
	opUnaryMinus
	opBNot
	opNot
	opLength
	opConcat
	opClose
	opTBC
	opJump
	opEqual
	opLessThan
	opLessOrEqual
	opEqualK
	opEqualI
	opLessThanI
	opLessOrEqualI
	opGreaterThanI
	opGreaterOrEqualI
	opTest
	opTestSet
	opCall
	opTailCall
	opReturn
	opReturn0
	opReturn1
	opForLoop
	opForPrep
	opTForPrep
	opTForCall
	opTForLoop
	opSetList
	opClosure
	opVarArg
	opVarArgPrep
	opExtraArg
)

var opNames = []string{
	"MOVE",
	"LOADI",
	"LOADF",
	"LOADK",
	"LOADKX",
	"LOADFALSE",
	"LFALSESKIP",
	"LOADTRUE",
	"LOADNIL",
	"GETUPVAL",
	"SETUPVAL",
	"GETTABUP",
	"GETTABLE",
	"GETI",
	"GETFIELD",
	"SETTABUP",
	"SETTABLE",
	"SETI",
	"SETFIELD",
	"NEWTABLE",
	"SELF",
	"ADDI",
	"ADDK",
	"SUBK",
	"MULK",
	"MODK",
	"POWK",
	"DIVK",
	"IDIVK",
	"BANDK",
	"BORK",
	"BXORK",
	"SHRI",
	"SHLI",
	"ADD",
	"SUB",
	"MUL",
	"MOD",
	"POW",
	"DIV",
	"IDIV",
	"BAND",
	"BOR",
	"BXOR",
	"SHL",
	"SHR",
	"MMBIN",
	"MMBINI",
	"MMBINK",
	"UNM",
	"BNOT",
	"NOT",
	"LEN",
	"CONCAT",
	"CLOSE",
	"TBC",
	"JMP",
	"EQ",
	"LT",
	"LE",
	"EQK",
	"EQI",
	"LTI",
	"LEI",
	"GTI",
	"GEI",
	"TEST",
	"TESTSET",
	"CALL",
	"TAILCALL",
	"RETURN",
	"RETURN0",
	"RETURN1",
	"FORLOOP",
	"FORPREP",
	"TFORPREP",
	"TFORCALL",
	"TFORLOOP",
	"SETLIST",
	"CLOSURE",
	"VARARG",
	"VARARGPREP",
	"EXTRAARG",
}

// Lua 5.4 instruction layout:
//   iABC:  op(7) | A(8) | k(1) | B(8) | C(8)
//   iABx:  op(7) | A(8) |     Bx(17)
//   iAsBx: op(7) | A(8) |    sBx(17)
//   iAx:   op(7) |         Ax(25)
//   isJ:   op(7) |         sJ(25)
const (
	sizeC  = 8
	sizeB  = 8
	sizeBx = sizeC + sizeB + 1 // 17 (includes k bit position)
	sizeA  = 8
	sizeAx = sizeBx + sizeA // 25
	sizeSJ = sizeAx         // 25
	sizeOp = 7

	posOp = 0
	posA  = posOp + sizeOp // 7
	posK  = posA + sizeA   // 15
	posB  = posK + 1       // 16
	posC  = posB + sizeB   // 24
	posBx = posK           // 15
	posAx = posA           // 7
	posSJ = posA           // 7

	maxArgAx  = 1<<sizeAx - 1
	maxArgBx  = 1<<sizeBx - 1
	maxArgSBx = maxArgBx >> 1 // sBx is signed
	maxArgSJ  = 1<<sizeSJ - 1
	offsetSJ  = maxArgSJ >> 1
	maxArgA   = 1<<sizeA - 1
	maxArgB   = 1<<sizeB - 1
	maxArgC   = 1<<sizeC - 1
	offsetSC  = maxArgC >> 1 // 127, for signed C and signed B

	noReg = maxArgA // 255, invalid register

	listItemsPerFlush = 50 // # list items to accumulate before a setList instruction
)

type instruction uint32

// creates a mask with 'n' 1 bits at position 'p'
func mask1(n, p uint) instruction { return ^(^instruction(0) << n) << p }

// creates a mask with 'n' 0 bits at position 'p'
func mask0(n, p uint) instruction { return ^mask1(n, p) }

func (i instruction) opCode() opCode         { return opCode(i >> posOp & (1<<sizeOp - 1)) }
func (i instruction) arg(pos, size uint) int { return int(i >> pos & mask1(size, 0)) }
func (i *instruction) setOpCode(op opCode)   { i.setArg(posOp, sizeOp, int(op)) }
func (i *instruction) setArg(pos, size uint, arg int) {
	*i = *i&mask0(size, pos) | instruction(arg)<<pos&mask1(size, pos)
}

// Manually inlined for performance (gc optimizer cannot inline through multiple calls)
func (i instruction) a() int   { return int(i >> posA & maxArgA) }
func (i instruction) b() int   { return int(i >> posB & maxArgB) }
func (i instruction) c() int   { return int(i >> posC & maxArgC) }
func (i instruction) bx() int  { return int(i >> posBx & maxArgBx) }
func (i instruction) ax() int  { return int(i >> posAx & maxArgAx) }
func (i instruction) sbx() int { return int(i>>posBx&maxArgBx) - maxArgSBx }

// Lua 5.4 new accessors
func (i instruction) k() int  { return int(i >> posK & 1) }
func (i instruction) sB() int { return i.b() - offsetSC }
func (i instruction) sC() int { return i.c() - offsetSC }
func (i instruction) sJ() int { return int(i>>posSJ&maxArgSJ) - offsetSJ }

func (i *instruction) setA(arg int)   { i.setArg(posA, sizeA, arg) }
func (i *instruction) setB(arg int)   { i.setArg(posB, sizeB, arg) }
func (i *instruction) setC(arg int)   { i.setArg(posC, sizeC, arg) }
func (i *instruction) setK(arg int)   { i.setArg(posK, 1, arg) }
func (i *instruction) setBx(arg int)  { i.setArg(posBx, sizeBx, arg) }
func (i *instruction) setAx(arg int)  { i.setArg(posAx, sizeAx, arg) }
func (i *instruction) setSBx(arg int) { i.setArg(posBx, sizeBx, arg+maxArgSBx) }
func (i *instruction) setSJ(arg int)  { i.setArg(posSJ, sizeSJ, arg+offsetSJ) }

func createABCk(op opCode, a, b, c, k int) instruction {
	return instruction(op)<<posOp |
		instruction(a)<<posA |
		instruction(b)<<posB |
		instruction(c)<<posC |
		instruction(k)<<posK
}

func createABx(op opCode, a, bx int) instruction {
	return instruction(op)<<posOp |
		instruction(a)<<posA |
		instruction(bx)<<posBx
}

func createAx(op opCode, a int) instruction {
	return instruction(op)<<posOp | instruction(a)<<posAx
}

func createSJ(op opCode, sj, k int) instruction {
	return instruction(op)<<posOp |
		instruction(sj+offsetSJ)<<posSJ |
		instruction(k)<<posK
}

func (i instruction) String() string {
	op := i.opCode()
	if int(op) >= len(opNames) {
		return fmt.Sprintf("UNKNOWN(%d)", op)
	}
	s := opNames[op]
	switch opMode(op) {
	case iABC:
		s = fmt.Sprintf("%s %d %d %d", s, i.a(), i.b(), i.c())
		if i.k() != 0 {
			s = fmt.Sprintf("%s (k)", s)
		}
	case iAsBx:
		s = fmt.Sprintf("%s %d %d", s, i.a(), i.sbx())
	case iABx:
		s = fmt.Sprintf("%s %d %d", s, i.a(), i.bx())
	case iAx:
		s = fmt.Sprintf("%s %d", s, i.ax())
	case isJ:
		s = fmt.Sprintf("%s %d", s, i.sJ())
	}
	return s
}

// Lua 5.4 opmode format:
//   bits 0-2: op mode (iABC=0, iABx=1, iAsBx=2, iAx=3, isJ=4)
//   bit 3: instruction sets register A
//   bit 4: operator is a test (next instruction must be a jump)
//   bit 5: instruction uses 'L->top' set by previous instruction (when B == 0)
//   bit 6: instruction sets 'L->top' for next instruction (when C == 0)
//   bit 7: instruction is an MM instruction (call a metamethod)
func opmode(mm, ot, it, t, a, m int) byte {
	return byte(mm<<7 | ot<<6 | it<<5 | t<<4 | a<<3 | m)
}

func opMode(m opCode) int      { return int(opModes[m] & 7) }
func testAMode(m opCode) bool  { return opModes[m]&(1<<3) != 0 }
func testTMode(m opCode) bool  { return opModes[m]&(1<<4) != 0 }
func testITMode(m opCode) bool { return opModes[m]&(1<<5) != 0 }
func testOTMode(m opCode) bool { return opModes[m]&(1<<6) != 0 }
func testMMMode(m opCode) bool { return opModes[m]&(1<<7) != 0 }

// isOT checks if instruction sets top for next instruction
func isOT(i instruction) bool {
	return (testOTMode(i.opCode()) && i.c() == 0) || i.opCode() == opTailCall
}

// isIT checks if instruction uses top from previous instruction
func isIT(i instruction) bool {
	return testITMode(i.opCode()) && i.b() == 0
}

var opModes = []byte{
	//       MM OT IT T  A  mode              opcode
	opmode(0, 0, 0, 0, 1, iABC),  // opMove
	opmode(0, 0, 0, 0, 1, iAsBx), // opLoadI
	opmode(0, 0, 0, 0, 1, iAsBx), // opLoadF
	opmode(0, 0, 0, 0, 1, iABx),  // opLoadConstant
	opmode(0, 0, 0, 0, 1, iABx),  // opLoadConstantEx
	opmode(0, 0, 0, 0, 1, iABC),  // opLoadFalse
	opmode(0, 0, 0, 0, 1, iABC),  // opLoadFalseSkip
	opmode(0, 0, 0, 0, 1, iABC),  // opLoadTrue
	opmode(0, 0, 0, 0, 1, iABC),  // opLoadNil
	opmode(0, 0, 0, 0, 1, iABC),  // opGetUpValue
	opmode(0, 0, 0, 0, 0, iABC),  // opSetUpValue
	opmode(0, 0, 0, 0, 1, iABC),  // opGetTableUp
	opmode(0, 0, 0, 0, 1, iABC),  // opGetTable
	opmode(0, 0, 0, 0, 1, iABC),  // opGetI
	opmode(0, 0, 0, 0, 1, iABC),  // opGetField
	opmode(0, 0, 0, 0, 0, iABC),  // opSetTableUp
	opmode(0, 0, 0, 0, 0, iABC),  // opSetTable
	opmode(0, 0, 0, 0, 0, iABC),  // opSetI
	opmode(0, 0, 0, 0, 0, iABC),  // opSetField
	opmode(0, 0, 0, 0, 1, iABC),  // opNewTable
	opmode(0, 0, 0, 0, 1, iABC),  // opSelf
	opmode(0, 0, 0, 0, 1, iABC),  // opAddI
	opmode(0, 0, 0, 0, 1, iABC),  // opAddK
	opmode(0, 0, 0, 0, 1, iABC),  // opSubK
	opmode(0, 0, 0, 0, 1, iABC),  // opMulK
	opmode(0, 0, 0, 0, 1, iABC),  // opModK
	opmode(0, 0, 0, 0, 1, iABC),  // opPowK
	opmode(0, 0, 0, 0, 1, iABC),  // opDivK
	opmode(0, 0, 0, 0, 1, iABC),  // opIDivK
	opmode(0, 0, 0, 0, 1, iABC),  // opBAndK
	opmode(0, 0, 0, 0, 1, iABC),  // opBOrK
	opmode(0, 0, 0, 0, 1, iABC),  // opBXorK
	opmode(0, 0, 0, 0, 1, iABC),  // opShrI
	opmode(0, 0, 0, 0, 1, iABC),  // opShlI
	opmode(0, 0, 0, 0, 1, iABC),  // opAdd
	opmode(0, 0, 0, 0, 1, iABC),  // opSub
	opmode(0, 0, 0, 0, 1, iABC),  // opMul
	opmode(0, 0, 0, 0, 1, iABC),  // opMod
	opmode(0, 0, 0, 0, 1, iABC),  // opPow
	opmode(0, 0, 0, 0, 1, iABC),  // opDiv
	opmode(0, 0, 0, 0, 1, iABC),  // opIDiv
	opmode(0, 0, 0, 0, 1, iABC),  // opBAnd
	opmode(0, 0, 0, 0, 1, iABC),  // opBOr
	opmode(0, 0, 0, 0, 1, iABC),  // opBXor
	opmode(0, 0, 0, 0, 1, iABC),  // opShl
	opmode(0, 0, 0, 0, 1, iABC),  // opShr
	opmode(1, 0, 0, 0, 0, iABC),  // opMMBin
	opmode(1, 0, 0, 0, 0, iABC),  // opMMBinI
	opmode(1, 0, 0, 0, 0, iABC),  // opMMBinK
	opmode(0, 0, 0, 0, 1, iABC),  // opUnaryMinus
	opmode(0, 0, 0, 0, 1, iABC),  // opBNot
	opmode(0, 0, 0, 0, 1, iABC),  // opNot
	opmode(0, 0, 0, 0, 1, iABC),  // opLength
	opmode(0, 0, 0, 0, 1, iABC),  // opConcat
	opmode(0, 0, 0, 0, 0, iABC),  // opClose
	opmode(0, 0, 0, 0, 0, iABC),  // opTBC
	opmode(0, 0, 0, 0, 0, isJ),   // opJump
	opmode(0, 0, 0, 1, 0, iABC),  // opEqual
	opmode(0, 0, 0, 1, 0, iABC),  // opLessThan
	opmode(0, 0, 0, 1, 0, iABC),  // opLessOrEqual
	opmode(0, 0, 0, 1, 0, iABC),  // opEqualK
	opmode(0, 0, 0, 1, 0, iABC),  // opEqualI
	opmode(0, 0, 0, 1, 0, iABC),  // opLessThanI
	opmode(0, 0, 0, 1, 0, iABC),  // opLessOrEqualI
	opmode(0, 0, 0, 1, 0, iABC),  // opGreaterThanI
	opmode(0, 0, 0, 1, 0, iABC),  // opGreaterOrEqualI
	opmode(0, 0, 0, 1, 0, iABC),  // opTest
	opmode(0, 0, 0, 1, 1, iABC),  // opTestSet
	opmode(0, 1, 1, 0, 1, iABC),  // opCall
	opmode(0, 1, 1, 0, 1, iABC),  // opTailCall
	opmode(0, 0, 1, 0, 0, iABC),  // opReturn
	opmode(0, 0, 0, 0, 0, iABC),  // opReturn0
	opmode(0, 0, 0, 0, 0, iABC),  // opReturn1
	opmode(0, 0, 0, 0, 1, iABx),  // opForLoop
	opmode(0, 0, 0, 0, 1, iABx),  // opForPrep
	opmode(0, 0, 0, 0, 0, iABx),  // opTForPrep
	opmode(0, 0, 0, 0, 0, iABC),  // opTForCall
	opmode(0, 0, 0, 0, 1, iABx),  // opTForLoop
	opmode(0, 0, 1, 0, 0, iABC),  // opSetList
	opmode(0, 0, 0, 0, 1, iABx),  // opClosure
	opmode(0, 1, 0, 0, 1, iABC),  // opVarArg
	opmode(0, 0, 1, 0, 1, iABC),  // opVarArgPrep
	opmode(0, 0, 0, 0, 0, iAx),   // opExtraArg
}
