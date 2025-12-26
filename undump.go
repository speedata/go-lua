package lua

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unsafe"
)

type loadState struct {
	in    io.Reader
	order binary.ByteOrder
}

// Lua 5.3 header format
var header struct {
	Signature                  [4]byte
	Version, Format            byte
	Data                       [6]byte // LUAC_DATA: "\x19\x93\r\n\x1a\n"
	IntSize, PointerSize       byte
	InstructionSize            byte
	IntegerSize, NumberSize    byte
	TestInt                    int64   // LUAC_INT: 0x5678
	TestNum                    float64 // LUAC_NUM: 370.5
}

var (
	errUnknownConstantType = errors.New("lua: unknown constant type in lua binary")
	errNotPrecompiledChunk = errors.New("lua: is not a precompiled chunk")
	errVersionMismatch     = errors.New("lua: version mismatch in precompiled chunk")
	errIncompatible        = errors.New("lua: incompatible precompiled chunk")
	errCorrupted           = errors.New("lua: corrupted precompiled chunk")
)

func (state *loadState) read(data interface{}) error {
	return binary.Read(state.in, state.order, data)
}

func (state *loadState) readNumber() (f float64, err error) {
	err = state.read(&f)
	return
}

func (state *loadState) readInteger() (i int64, err error) {
	err = state.read(&i)
	return
}

func (state *loadState) readInt() (i int32, err error) {
	err = state.read(&i)
	return
}

func (state *loadState) readPC() (pc, error) {
	i, err := state.readInt()
	return pc(i), err
}

func (state *loadState) readByte() (b byte, err error) {
	err = state.read(&b)
	return
}

func (state *loadState) readBool() (bool, error) {
	b, err := state.readByte()
	return b != 0, err
}

func (state *loadState) readString() (s string, err error) {
	// Lua 5.3: 1-byte prefix for short strings, 0xFF + size_t for long strings
	var sizeByte byte
	if sizeByte, err = state.readByte(); err != nil || sizeByte == 0 {
		return
	}

	var size uint64
	if sizeByte == 0xFF {
		// Long string: read full size_t
		maxUint := ^uint(0)
		if uint64(maxUint) == math.MaxUint64 {
			var size64 uint64
			if err = state.read(&size64); err != nil {
				return
			}
			size = size64
		} else {
			var size32 uint32
			if err = state.read(&size32); err != nil {
				return
			}
			size = uint64(size32)
		}
	} else {
		// Short string: size is in the byte (1-254)
		size = uint64(sizeByte)
	}

	// Size includes the terminating NUL, but Lua 5.3 doesn't write NUL
	if size == 0 {
		return
	}
	ba := make([]byte, size-1)
	if err = state.read(ba); err == nil {
		s = string(ba)
	}
	return
}

func (state *loadState) readCode() (code []instruction, err error) {
	n, err := state.readInt()
	if err != nil || n == 0 {
		return
	}
	code = make([]instruction, n)
	err = state.read(code)
	return
}

func (state *loadState) readUpValues() (u []upValueDesc, err error) {
	n, err := state.readInt()
	if err != nil || n == 0 {
		return
	}
	v := make([]struct{ IsLocal, Index byte }, n)
	err = state.read(v)
	if err != nil {
		return
	}
	u = make([]upValueDesc, n)
	for i := range v {
		u[i].isLocal, u[i].index = v[i].IsLocal != 0, int(v[i].Index)
	}
	return
}

func (state *loadState) readLocalVariables() (localVariables []localVariable, err error) {
	var n int32
	if n, err = state.readInt(); err != nil || n == 0 {
		return
	}
	localVariables = make([]localVariable, n)
	for i := range localVariables {
		if localVariables[i].name, err = state.readString(); err != nil {
			return
		}
		if localVariables[i].startPC, err = state.readPC(); err != nil {
			return
		}
		if localVariables[i].endPC, err = state.readPC(); err != nil {
			return
		}
	}
	return
}

func (state *loadState) readLineInfo() (lineInfo []int32, err error) {
	var n int32
	if n, err = state.readInt(); err != nil || n == 0 {
		return
	}
	lineInfo = make([]int32, n)
	err = state.read(lineInfo)
	return
}

func (state *loadState) readDebug(p *prototype) (source string, lineInfo []int32, localVariables []localVariable, names []string, err error) {
	var n int32
	if source, err = state.readString(); err != nil {
		return
	}
	if lineInfo, err = state.readLineInfo(); err != nil {
		return
	}
	if localVariables, err = state.readLocalVariables(); err != nil {
		return
	}
	if n, err = state.readInt(); err != nil {
		return
	}
	names = make([]string, n)
	for i := range names {
		if names[i], err = state.readString(); err != nil {
			return
		}
	}
	return
}

// readDebug53 reads Lua 5.3 debug info (source is read earlier in function)
func (state *loadState) readDebug53(p *prototype) (lineInfo []int32, localVariables []localVariable, err error) {
	var n int32
	if lineInfo, err = state.readLineInfo(); err != nil {
		return
	}
	if localVariables, err = state.readLocalVariables(); err != nil {
		return
	}
	// Read upvalue names
	if n, err = state.readInt(); err != nil {
		return
	}
	for i := 0; i < int(n) && i < len(p.upValues); i++ {
		if p.upValues[i].name, err = state.readString(); err != nil {
			return
		}
	}
	return
}

// Lua 5.3 type tags for constants
const (
	luaTNil     = 0x00
	luaTBoolean = 0x01
	luaTNumFlt  = 0x03 // LUA_TNUMFLT: float constant
	luaTNumInt  = 0x13 // LUA_TNUMINT: integer constant (0x03 | (1 << 4))
	luaTShrStr  = 0x04 // LUA_TSHRSTR: short string
	luaTLngStr  = 0x14 // LUA_TLNGSTR: long string (0x04 | (1 << 4))
)

func (state *loadState) readConstants() (constants []value, prototypes []prototype, err error) {
	var n int32
	if n, err = state.readInt(); err != nil || n == 0 {
		return
	}

	constants = make([]value, n)
	for i := range constants {
		var t byte
		switch t, err = state.readByte(); {
		case err != nil:
			return
		case t == luaTNil:
			constants[i] = nil
		case t == luaTBoolean:
			constants[i], err = state.readBool()
		case t == luaTNumFlt:
			constants[i], err = state.readNumber()
		case t == luaTNumInt:
			constants[i], err = state.readInteger()
		case t == luaTShrStr || t == luaTLngStr:
			constants[i], err = state.readString()
		default:
			err = errUnknownConstantType
		}
		if err != nil {
			return
		}
	}
	return
}

func (state *loadState) readPrototypes() (prototypes []prototype, err error) {
	var n int32
	if n, err = state.readInt(); err != nil || n == 0 {
		return
	}
	prototypes = make([]prototype, n)
	for i := range prototypes {
		if prototypes[i], err = state.readFunction(); err != nil {
			return
		}
	}
	return
}

func (state *loadState) readFunction() (p prototype, err error) {
	// Lua 5.3 function format: source first, then rest
	if p.source, err = state.readString(); err != nil {
		return
	}
	var n int32
	if n, err = state.readInt(); err != nil {
		return
	}
	p.lineDefined = int(n)
	if n, err = state.readInt(); err != nil {
		return
	}
	p.lastLineDefined = int(n)
	var b byte
	if b, err = state.readByte(); err != nil {
		return
	}
	p.parameterCount = int(b)
	if b, err = state.readByte(); err != nil {
		return
	}
	p.isVarArg = b != 0
	if b, err = state.readByte(); err != nil {
		return
	}
	p.maxStackSize = int(b)
	if p.code, err = state.readCode(); err != nil {
		return
	}
	// Lua 5.3: constants, upvalues, prototypes (not constants+prototypes together)
	if p.constants, _, err = state.readConstants(); err != nil {
		return
	}
	if p.upValues, err = state.readUpValues(); err != nil {
		return
	}
	if p.prototypes, err = state.readPrototypes(); err != nil {
		return
	}
	// Lua 5.3: debug info without source (source is at start)
	if p.lineInfo, p.localVariables, err = state.readDebug53(&p); err != nil {
		return
	}
	return
}

func init() {
	copy(header.Signature[:], Signature)
	header.Version = VersionMajor<<4 | VersionMinor
	header.Format = 0
	data := "\x19\x93\r\n\x1a\n"
	copy(header.Data[:], data)
	header.IntSize = 4
	header.PointerSize = byte(1+^uintptr(0)>>32&1) * 4
	header.InstructionSize = byte(1+^instruction(0)>>32&1) * 4
	header.IntegerSize = 8 // sizeof(lua_Integer) = int64
	header.NumberSize = 8  // sizeof(lua_Number) = float64
	header.TestInt = 0x5678
	header.TestNum = 370.5

	// The uintptr numeric type is implementation-specific
	uintptrBitCount := byte(0)
	for bits := ^uintptr(0); bits != 0; bits >>= 1 {
		uintptrBitCount++
	}
	if uintptrBitCount != header.PointerSize*8 {
		panic(fmt.Sprintf("invalid pointer size (%d)", uintptrBitCount))
	}
}

func endianness() binary.ByteOrder {
	if x := 1; *(*byte)(unsafe.Pointer(&x)) == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func (state *loadState) checkHeader() error {
	h := header
	if err := state.read(&h); err != nil {
		return err
	} else if h == header {
		return nil
	} else if string(h.Signature[:]) != Signature {
		return errNotPrecompiledChunk
	} else if h.Version != header.Version || h.Format != header.Format {
		return errVersionMismatch
	} else if h.Data != header.Data {
		return errCorrupted
	}
	return errIncompatible
}

func (l *State) undump(in io.Reader, name string) (c *luaClosure, err error) {
	if name[0] == '@' || name[0] == '=' {
		name = name[1:]
	} else if name[0] == Signature[0] {
		name = "binary string"
	}
	// TODO assign name to p.source?
	s := &loadState{in, endianness()}
	var p prototype
	if err = s.checkHeader(); err != nil {
		return
	}
	// Lua 5.3: read upvalue count byte after header
	if _, err = s.readByte(); err != nil {
		return
	}
	if p, err = s.readFunction(); err != nil {
		return
	}
	c = l.newLuaClosure(&p)
	l.push(c)
	return
}
