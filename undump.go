package lua

import (
	"encoding/binary"
	"errors"
	"io"
	"unsafe"
)

type loadState struct {
	in    io.Reader
	order binary.ByteOrder
}

// Lua 5.4 header: no IntSize/PointerSize fields
var header54 struct {
	Signature                   [4]byte
	Version, Format             byte
	Data                        [6]byte // LUAC_DATA: "\x19\x93\r\n\x1a\n"
	InstructionSize             byte
	IntegerSize, NumberSize     byte
	TestInt                     int64   // LUAC_INT: 0x5678
	TestNum                     float64 // LUAC_NUM: 370.5
}

var (
	errUnknownConstantType = errors.New("lua: unknown constant type in lua binary")
	errNotPrecompiledChunk = errors.New("lua: is not a precompiled chunk")
	errVersionMismatch     = errors.New("lua: version mismatch in precompiled chunk")
	errIncompatible        = errors.New("lua: incompatible precompiled chunk")
	errCorrupted           = errors.New("lua: corrupted precompiled chunk")
	errTruncated           = errors.New("truncated")
	errIntegerOverflow     = errors.New("lua: integer overflow in precompiled chunk")
)

func (state *loadState) read(data interface{}) error {
	if err := binary.Read(state.in, state.order, data); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errTruncated
		}
		return err
	}
	return nil
}

func (state *loadState) readNumber() (f float64, err error) {
	err = state.read(&f)
	return
}

func (state *loadState) readInteger() (i int64, err error) {
	err = state.read(&i)
	return
}

func (state *loadState) readByte() (b byte, err error) {
	err = state.read(&b)
	return
}

// readUnsigned reads a variable-length unsigned integer (Lua 5.4 format).
// Each byte contributes 7 bits; MSB (0x80) set means this is the last byte.
func (state *loadState) readUnsigned(limit uint64) (uint64, error) {
	var x uint64
	limit >>= 7
	for {
		b, err := state.readByte()
		if err != nil {
			return 0, err
		}
		if x >= limit {
			return 0, errIntegerOverflow
		}
		x = (x << 7) | uint64(b&0x7f)
		if b&0x80 != 0 {
			return x, nil
		}
	}
}

func (state *loadState) readSize() (int, error) {
	n, err := state.readUnsigned(^uint64(0))
	return int(n), err
}

func (state *loadState) readInt() (int, error) {
	n, err := state.readUnsigned(uint64(maxInt))
	return int(n), err
}

func (state *loadState) readString() (s string, err error) {
	size, err := state.readSize()
	if err != nil || size == 0 {
		return
	}
	// size includes conceptual NUL; actual data is size-1 bytes
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
	// Lua 5.4: 3 bytes per upvalue (instack, idx, kind)
	u = make([]upValueDesc, n)
	for i := range u {
		var instack, idx, kind byte
		if instack, err = state.readByte(); err != nil {
			return
		}
		if idx, err = state.readByte(); err != nil {
			return
		}
		if kind, err = state.readByte(); err != nil {
			return
		}
		u[i].isLocal = instack != 0
		u[i].index = int(idx)
		u[i].kind = kind
	}
	return
}

func (state *loadState) readLocalVariables() (localVariables []localVariable, err error) {
	var n int
	if n, err = state.readInt(); err != nil || n == 0 {
		return
	}
	localVariables = make([]localVariable, n)
	for i := range localVariables {
		if localVariables[i].name, err = state.readString(); err != nil {
			return
		}
		startPC, e := state.readInt()
		if e != nil {
			err = e
			return
		}
		localVariables[i].startPC = pc(startPC)
		endPC, e := state.readInt()
		if e != nil {
			err = e
			return
		}
		localVariables[i].endPC = pc(endPC)
	}
	return
}

// readDebug54 reads Lua 5.4 debug info (split lineinfo)
func (state *loadState) readDebug54(p *prototype) error {
	// Relative line info (int8 per instruction)
	n, err := state.readInt()
	if err != nil {
		return err
	}
	if n > 0 {
		p.lineInfo = make([]int8, n)
		if err = state.read(p.lineInfo); err != nil {
			return err
		}
	}

	// Absolute line info
	n, err = state.readInt()
	if err != nil {
		return err
	}
	if n > 0 {
		p.absLineInfos = make([]absLineInfo, n)
		for i := range p.absLineInfos {
			if p.absLineInfos[i].pc, err = state.readInt(); err != nil {
				return err
			}
			if p.absLineInfos[i].line, err = state.readInt(); err != nil {
				return err
			}
		}
	}

	// Local variables
	p.localVariables, err = state.readLocalVariables()
	if err != nil {
		return err
	}

	// Upvalue names
	n, err = state.readInt()
	if err != nil {
		return err
	}
	for i := 0; i < n && i < len(p.upValues); i++ {
		if p.upValues[i].name, err = state.readString(); err != nil {
			return err
		}
	}
	return nil
}

// Lua 5.4 type tags for constants
const (
	luaVNil    = 0x00 // LUA_VNIL
	luaVFalse  = 0x01 // LUA_VFALSE = makevariant(1, 0)
	luaVTrue   = 0x11 // LUA_VTRUE  = makevariant(1, 1)
	luaVNumInt = 0x03 // LUA_VNUMINT = makevariant(3, 0)
	luaVNumFlt = 0x13 // LUA_VNUMFLT = makevariant(3, 1)
	luaVShrStr = 0x04 // LUA_VSHRSTR = makevariant(4, 0)
	luaVLngStr = 0x14 // LUA_VLNGSTR = makevariant(4, 1)
)

func (state *loadState) readConstants() (constants []value, err error) {
	n, err := state.readInt()
	if err != nil || n == 0 {
		return
	}

	constants = make([]value, n)
	for i := range constants {
		var t byte
		switch t, err = state.readByte(); {
		case err != nil:
			return
		case t == luaVNil:
			constants[i] = nil
		case t == luaVFalse:
			constants[i] = false
		case t == luaVTrue:
			constants[i] = true
		case t == luaVNumInt:
			constants[i], err = state.readInteger()
		case t == luaVNumFlt:
			constants[i], err = state.readNumber()
		case t == luaVShrStr || t == luaVLngStr:
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

func (state *loadState) readPrototypes(psource string) (prototypes []prototype, err error) {
	n, err := state.readInt()
	if err != nil || n == 0 {
		return
	}
	prototypes = make([]prototype, n)
	for i := range prototypes {
		if prototypes[i], err = state.readFunction(psource); err != nil {
			return
		}
	}
	return
}

func (state *loadState) readFunction(psource string) (p prototype, err error) {
	// Lua 5.4: source first (nullable, inherits from parent).
	// A NULL source (size 0 in dump) means "inherit from parent" or
	// "no source" (stripped). We read the size directly to distinguish
	// NULL (size=0) from an explicitly empty string (size=1).
	sourceSize, err := state.readSize()
	if err != nil {
		return
	}
	if sourceSize == 0 {
		// NULL source: inherit from parent, or "=?" if no parent
		if psource != "" {
			p.source = psource
		} else {
			p.source = "=?"
		}
	} else {
		ba := make([]byte, sourceSize-1)
		if err = state.read(ba); err != nil {
			return
		}
		p.source = string(ba)
	}
	var n int
	if n, err = state.readInt(); err != nil {
		return
	}
	p.lineDefined = n
	if n, err = state.readInt(); err != nil {
		return
	}
	p.lastLineDefined = n
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
	// Lua 5.4: constants, upvalues, prototypes, debug
	if p.constants, err = state.readConstants(); err != nil {
		return
	}
	if p.upValues, err = state.readUpValues(); err != nil {
		return
	}
	if p.prototypes, err = state.readPrototypes(p.source); err != nil {
		return
	}
	if err = state.readDebug54(&p); err != nil {
		return
	}
	return
}

func init() {
	copy(header54.Signature[:], Signature)
	header54.Version = VersionMajor<<4 | VersionMinor
	header54.Format = 0
	data := "\x19\x93\r\n\x1a\n"
	copy(header54.Data[:], data)
	header54.InstructionSize = 4 // sizeof(Instruction) = uint32
	header54.IntegerSize = 8     // sizeof(lua_Integer) = int64
	header54.NumberSize = 8      // sizeof(lua_Number) = float64
	header54.TestInt = 0x5678
	header54.TestNum = 370.5
}

func endianness() binary.ByteOrder {
	if x := 1; *(*byte)(unsafe.Pointer(&x)) == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func (state *loadState) checkHeader() error {
	h := header54
	if err := state.read(&h); err != nil {
		return err
	} else if h == header54 {
		return nil
	} else if string(h.Signature[:]) != Signature {
		return errNotPrecompiledChunk
	} else if h.Version != header54.Version || h.Format != header54.Format {
		return errVersionMismatch
	} else if h.Data != header54.Data {
		return errCorrupted
	}
	return errIncompatible
}

func (l *State) undump(in io.Reader, name string) (c *luaClosure, err error) {
	if len(name) > 0 {
		if name[0] == '@' || name[0] == '=' {
			name = name[1:]
		} else if name[0] == Signature[0] {
			name = "binary string"
		}
	}
	s := &loadState{in, endianness()}
	var p prototype
	if err = s.checkHeader(); err != nil {
		return
	}
	// Lua 5.4: read upvalue count byte after header
	if _, err = s.readByte(); err != nil {
		return
	}
	if p, err = s.readFunction(""); err != nil {
		return
	}
	c = l.newLuaClosure(&p)
	l.push(c)
	return
}
