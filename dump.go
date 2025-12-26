package lua

import (
	"encoding/binary"
	"fmt"
	"io"
)

type dumpState struct {
	l     *State
	out   io.Writer
	order binary.ByteOrder
	err   error
}

func (d *dumpState) write(data interface{}) {
	if d.err == nil {
		d.err = binary.Write(d.out, d.order, data)
	}
}

func (d *dumpState) writeInt(i int) {
	d.write(int32(i))
}

func (d *dumpState) writePC(p pc) {
	d.writeInt(int(p))
}

func (d *dumpState) writeCode(p *prototype) {
	d.writeInt(len(p.code))
	d.write(p.code)
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

// Lua 5.3 type tags for constants
const (
	dumpTNil     = 0x00
	dumpTBoolean = 0x01
	dumpTNumFlt  = 0x03 // LUA_TNUMFLT
	dumpTNumInt  = 0x13 // LUA_TNUMINT
	dumpTShrStr  = 0x04 // LUA_TSHRSTR
	dumpTLngStr  = 0x14 // LUA_TLNGSTR

	// LUAI_MAXSHORTLEN: max length for short strings (interned)
	maxShortLen = 40
)

func (d *dumpState) writeConstants(p *prototype) {
	d.writeInt(len(p.constants))

	for _, o := range p.constants {
		switch v := o.(type) {
		case nil:
			d.writeByte(dumpTNil)
		case bool:
			d.writeByte(dumpTBoolean)
			d.writeBool(v)
		case int64:
			d.writeByte(dumpTNumInt)
			d.writeInteger(v)
		case float64:
			d.writeByte(dumpTNumFlt)
			d.writeNumber(v)
		case string:
			// Lua 5.3: short strings <= 40 chars, long strings > 40 chars
			if len(v) <= maxShortLen {
				d.writeByte(dumpTShrStr)
			} else {
				d.writeByte(dumpTLngStr)
			}
			d.writeString(v)
		default:
			d.l.assert(false)
		}
	}
}

func (d *dumpState) writePrototypes(p *prototype) {
	d.writeInt(len(p.prototypes))

	for _, o := range p.prototypes {
		d.dumpFunction(&o)
	}
}

func (d *dumpState) writeUpvalues(p *prototype) {
	d.writeInt(len(p.upValues))

	for _, u := range p.upValues {
		d.writeBool(u.isLocal)
		d.writeByte(byte(u.index))
	}
}

func (d *dumpState) writeString(s string) {
	// Lua 5.3: 1-byte prefix for short strings (1-254), 0xFF + size_t for long
	ba := []byte(s)
	size := len(s)
	if size == 0 {
		d.writeByte(0)
		return
	}
	size++ // Size includes conceptual NUL (though not written)

	if size < 0xFF {
		d.writeByte(byte(size))
	} else {
		d.writeByte(0xFF)
		switch header.PointerSize {
		case 8:
			d.write(uint64(size))
		case 4:
			d.write(uint32(size))
		default:
			panic(fmt.Sprintf("unsupported pointer size (%d)", header.PointerSize))
		}
	}
	d.write(ba)
}

func (d *dumpState) writeLocalVariables(p *prototype) {
	d.writeInt(len(p.localVariables))

	for _, lv := range p.localVariables {
		d.writeString(lv.name)
		d.writePC(lv.startPC)
		d.writePC(lv.endPC)
	}
}

// writeDebug53 writes Lua 5.3 debug info (source is written at start of function)
func (d *dumpState) writeDebug53(p *prototype) {
	d.writeInt(len(p.lineInfo))
	d.write(p.lineInfo)
	d.writeLocalVariables(p)

	d.writeInt(len(p.upValues))

	for _, uv := range p.upValues {
		d.writeString(uv.name)
	}
}

func (d *dumpState) dumpFunction(p *prototype) {
	// Lua 5.3: source first
	d.writeString(p.source)
	d.writeInt(p.lineDefined)
	d.writeInt(p.lastLineDefined)
	d.writeByte(byte(p.parameterCount))
	d.writeBool(p.isVarArg)
	d.writeByte(byte(p.maxStackSize))
	d.writeCode(p)
	// Lua 5.3: constants, upvalues, prototypes (not constants+prototypes together)
	d.writeConstants(p)
	d.writeUpvalues(p)
	d.writePrototypes(p)
	d.writeDebug53(p)
}

func (d *dumpState) dumpHeader() {
	d.err = binary.Write(d.out, d.order, header)
}

func (l *State) dump(p *prototype, w io.Writer) error {
	d := dumpState{l: l, out: w, order: endianness()}
	d.dumpHeader()
	// Lua 5.3: write upvalue count byte after header
	d.writeByte(byte(len(p.upValues)))
	d.dumpFunction(p)

	return d.err
}
