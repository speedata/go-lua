package lua

import (
	"encoding/binary"
	"io"
)

type dumpState struct {
	l     *State
	out   io.Writer
	order binary.ByteOrder
	err   error
	strip bool // strip debug information
}

func (d *dumpState) write(data interface{}) {
	if d.err == nil {
		d.err = binary.Write(d.out, d.order, data)
	}
}

func (d *dumpState) writeByte(b byte) {
	d.write(b)
}

func (d *dumpState) writeBool(b bool) {
	if b {
		d.writeByte(1)
	} else {
		d.writeByte(0)
	}
}

func (d *dumpState) writeNumber(f float64) {
	d.write(f)
}

func (d *dumpState) writeInteger(i int64) {
	d.write(i)
}

// writeSize writes a variable-length unsigned integer (Lua 5.4 format).
// Each byte contributes 7 bits; MSB (0x80) set means this is the last byte.
func (d *dumpState) writeSize(x int) {
	d.writeUnsigned(uint64(x))
}

func (d *dumpState) writeInt(x int) {
	d.writeSize(x)
}

func (d *dumpState) writeUnsigned(x uint64) {
	if d.err != nil {
		return
	}
	// Buffer size: each byte stores 7 bits, max 10 bytes for 64-bit
	var buff [10]byte
	n := 0
	for {
		buff[9-n] = byte(x & 0x7f)
		n++
		x >>= 7
		if x == 0 {
			break
		}
	}
	buff[9] |= 0x80 // mark last byte
	d.write(buff[10-n:])
}

func (d *dumpState) writeCode(p *prototype) {
	d.writeInt(len(p.code))
	d.write(p.code)
}

// Lua 5.4 type tags for constants (dump)
const (
	dumpVNil    = 0x00 // LUA_VNIL
	dumpVFalse  = 0x01 // LUA_VFALSE
	dumpVTrue   = 0x11 // LUA_VTRUE
	dumpVNumInt = 0x03 // LUA_VNUMINT
	dumpVNumFlt = 0x13 // LUA_VNUMFLT
	dumpVShrStr = 0x04 // LUA_VSHRSTR
	dumpVLngStr = 0x14 // LUA_VLNGSTR

	// LUAI_MAXSHORTLEN: max length for short strings (interned)
	maxShortLen = 40
)

func (d *dumpState) writeConstants(p *prototype) {
	d.writeInt(len(p.constants))

	for _, o := range p.constants {
		switch v := o.(type) {
		case nil:
			d.writeByte(dumpVNil)
		case bool:
			if v {
				d.writeByte(dumpVTrue)
			} else {
				d.writeByte(dumpVFalse)
			}
		case int64:
			d.writeByte(dumpVNumInt)
			d.writeInteger(v)
		case float64:
			d.writeByte(dumpVNumFlt)
			d.writeNumber(v)
		case string:
			if len(v) <= maxShortLen {
				d.writeByte(dumpVShrStr)
			} else {
				d.writeByte(dumpVLngStr)
			}
			d.writeStringValue(v)
		default:
			d.l.assert(false)
		}
	}
}

func (d *dumpState) writePrototypes(p *prototype) {
	d.writeInt(len(p.prototypes))

	for _, o := range p.prototypes {
		d.dumpFunction(&o, p.source)
	}
}

func (d *dumpState) writeUpvalues(p *prototype) {
	d.writeInt(len(p.upValues))

	// Lua 5.4: 3 bytes per upvalue (instack, idx, kind)
	for _, u := range p.upValues {
		d.writeBool(u.isLocal)
		d.writeByte(byte(u.index))
		d.writeByte(u.kind)
	}
}

// writeString writes a nullable string using Lua 5.4 variable-length size.
// Empty Go string is treated as NULL (size=0).
func (d *dumpState) writeString(s string) {
	if s == "" {
		d.writeSize(0)
		return
	}
	d.writeSize(len(s) + 1) // size includes conceptual NUL
	d.write([]byte(s))
}

// writeStringValue writes a non-null string value.
// Empty Go string "" is written as empty Lua string (size=1), not NULL.
func (d *dumpState) writeStringValue(s string) {
	d.writeSize(len(s) + 1) // 0+1=1 for empty string, len+1 for non-empty
	if len(s) > 0 {
		d.write([]byte(s))
	}
}

func (d *dumpState) writeLocalVariables(p *prototype) {
	d.writeInt(len(p.localVariables))

	for _, lv := range p.localVariables {
		d.writeString(lv.name)
		d.writeInt(int(lv.startPC))
		d.writeInt(int(lv.endPC))
	}
}

// writeDebug54Stripped writes empty debug info for stripped dumps.
func (d *dumpState) writeDebug54Stripped(p *prototype) {
	d.writeInt(0) // no relative line info
	d.writeInt(0) // no absolute line info
	d.writeInt(0) // no local variables
	d.writeInt(0) // no upvalue names
}

// writeDebug54 writes Lua 5.4 debug info (split lineinfo)
func (d *dumpState) writeDebug54(p *prototype) {
	// Relative line info
	d.writeInt(len(p.lineInfo))
	if len(p.lineInfo) > 0 {
		d.write(p.lineInfo)
	}

	// Absolute line info
	d.writeInt(len(p.absLineInfos))
	for _, ali := range p.absLineInfos {
		d.writeInt(ali.pc)
		d.writeInt(ali.line)
	}

	// Local variables
	d.writeLocalVariables(p)

	// Upvalue names
	d.writeInt(len(p.upValues))
	for _, uv := range p.upValues {
		d.writeString(uv.name)
	}
}

func (d *dumpState) dumpFunction(p *prototype, psource string) {
	// Lua 5.4: source first (nullable); stripped or same as parent = NULL.
	// Like C Lua, child functions write NULL when source matches parent.
	if d.strip || p.source == psource {
		d.writeString("") // NULL: size 0
	} else {
		d.writeStringValue(p.source)
	}
	d.writeInt(p.lineDefined)
	d.writeInt(p.lastLineDefined)
	d.writeByte(byte(p.parameterCount))
	d.writeBool(p.isVarArg)
	d.writeByte(byte(p.maxStackSize))
	d.writeCode(p)
	d.writeConstants(p)
	d.writeUpvalues(p)
	d.writePrototypes(p)
	if d.strip {
		d.writeDebug54Stripped(p)
	} else {
		d.writeDebug54(p)
	}
}

func (d *dumpState) dumpHeader() {
	d.err = binary.Write(d.out, d.order, header54)
}

func (l *State) dump(p *prototype, w io.Writer, strip bool) error {
	d := dumpState{l: l, out: w, order: endianness(), strip: strip}
	d.dumpHeader()
	// Lua 5.4: write upvalue count byte after header
	d.writeByte(byte(len(p.upValues)))
	d.dumpFunction(p, "")

	return d.err
}
