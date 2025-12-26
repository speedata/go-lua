package lua

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unsafe"
)

func relativePosition(pos, length int) int {
	if pos >= 0 {
		return pos
	} else if -pos > length {
		return 0
	}
	return length + pos + 1
}

// Pattern matching constants
const (
	patternMaxCaptures = 32
	patternSpecials    = "^$*+?.([%-"
)

// maxStringSize is the maximum size of strings created by string operations.
// This matches Lua 5.3's MAX_SIZE which is typically limited to ~2GB to match
// 32-bit int limits (even on 64-bit systems) for compatibility.
const maxStringSize = 0x7FFFFFFF // 2^31 - 1

// Capture represents a captured substring
type capture struct {
	start int // start position (0-based), -1 for position capture
	end   int // end position (0-based), -1 for unfinished
}

// matchState holds the state during pattern matching
type matchState struct {
	l           *State
	matchDepth  int
	src         string
	srcEnd      int
	pattern     string
	captures    []capture
	numCaptures int
}

const maxMatchDepth = 200

// Check if character c matches character class cl
func matchClass(c byte, cl byte) bool {
	var res bool
	switch cl | 0x20 { // lowercase
	case 'a':
		res = (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	case 'c':
		res = c < 32 || c == 127
	case 'd':
		res = c >= '0' && c <= '9'
	case 'g':
		res = c > 32 && c < 127
	case 'l':
		res = c >= 'a' && c <= 'z'
	case 'p':
		res = (c >= 33 && c <= 47) || (c >= 58 && c <= 64) ||
			(c >= 91 && c <= 96) || (c >= 123 && c <= 126)
	case 's':
		res = c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
	case 'u':
		res = c >= 'A' && c <= 'Z'
	case 'w':
		res = (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
	case 'x':
		res = (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
	case 'z':
		res = c == 0
	default:
		return c == cl
	}
	// Uppercase class = complement
	if cl >= 'A' && cl <= 'Z' {
		return !res
	}
	return res
}

// Find end of character class [...], returns index after ]
// Returns -1 if malformed (missing ])
func classEnd(pattern string, p int) int {
	p++ // skip '['
	if p < len(pattern) && pattern[p] == '^' {
		p++
	}
	// First ] after [ or [^ is literal, not end of class
	if p < len(pattern) && pattern[p] == ']' {
		p++ // skip literal ]
	}
	for {
		if p >= len(pattern) {
			return -1 // malformed: missing ]
		}
		c := pattern[p]
		p++
		if c == ']' {
			return p
		}
		if c == '%' {
			if p >= len(pattern) {
				return -1 // malformed: ends with %
			}
			p++ // skip escaped char
		}
	}
}

// Check if character c matches the class at pattern[p]
// Returns (matched, next position in pattern)
func (ms *matchState) singleMatch(c byte, p int) (bool, int) {
	if p >= len(ms.pattern) {
		return false, p
	}
	switch ms.pattern[p] {
	case '.':
		return true, p + 1
	case '%':
		if p+1 >= len(ms.pattern) {
			return false, p + 1
		}
		return matchClass(c, ms.pattern[p+1]), p + 2
	case '[':
		end := classEnd(ms.pattern, p)
		if end < 0 {
			Errorf(ms.l, "malformed pattern (missing ']')")
		}
		return ms.matchBracketClass(c, p, end), end
	default:
		return c == ms.pattern[p], p + 1
	}
}

// Match character against bracket class [...]
func (ms *matchState) matchBracketClass(c byte, p, end int) bool {
	sig := true
	p++ // skip '['
	if p < end && ms.pattern[p] == '^' {
		sig = false
		p++
	}
	// First ] after [ or [^ is literal
	if p < end-1 && ms.pattern[p] == ']' {
		if c == ']' {
			return sig
		}
		p++
	}
	for p < end-1 {
		if ms.pattern[p] == '%' {
			p++
			if p < end-1 && matchClass(c, ms.pattern[p]) {
				return sig
			}
			p++
		} else if p+2 < end-1 && ms.pattern[p+1] == '-' {
			// Range a-z (but not if - is at end before ])
			if c >= ms.pattern[p] && c <= ms.pattern[p+2] {
				return sig
			}
			p += 3
		} else {
			if c == ms.pattern[p] {
				return sig
			}
			p++
		}
	}
	return !sig
}

// Start a new capture
func (ms *matchState) startCapture(s, p int, what int) (int, bool) {
	if ms.numCaptures >= patternMaxCaptures {
		Errorf(ms.l, "too many captures")
	}
	ms.captures = append(ms.captures, capture{start: s, end: what})
	ms.numCaptures++
	res, ok := ms.match(s, p)
	if !ok {
		ms.numCaptures--
		ms.captures = ms.captures[:len(ms.captures)-1]
	}
	return res, ok
}

// End a capture
func (ms *matchState) endCapture(s, p int) (int, bool) {
	// Find the most recent unfinished capture
	for i := ms.numCaptures - 1; i >= 0; i-- {
		if ms.captures[i].end == -1 {
			ms.captures[i].end = s
			res, ok := ms.match(s, p)
			if !ok {
				ms.captures[i].end = -1
			}
			return res, ok
		}
	}
	Errorf(ms.l, "invalid pattern capture")
	return 0, false
}

// Match balanced pair %bxy
func (ms *matchState) matchBalance(s, p int) (int, bool) {
	if p+1 >= len(ms.pattern) {
		Errorf(ms.l, "malformed pattern (missing arguments to '%%b')")
	}
	open, close := ms.pattern[p], ms.pattern[p+1]
	if s >= ms.srcEnd || ms.src[s] != open {
		return 0, false
	}
	count := 1
	s++
	for s < ms.srcEnd {
		if ms.src[s] == close {
			count--
			if count == 0 {
				return s + 1, true
			}
		} else if ms.src[s] == open {
			count++
		}
		s++
	}
	return 0, false
}

// Get capture reference %1-%9
func (ms *matchState) checkCapture(c byte) int {
	if c < '1' || c > '9' {
		Errorf(ms.l, "invalid capture index %%"+string(c))
	}
	n := int(c - '1')
	// C Lua: all three conditions produce "invalid capture index %N"
	if n >= ms.numCaptures || ms.captures[n].end == -1 {
		Errorf(ms.l, "invalid capture index %%%d", n+1)
	}
	return n
}

// Match against captured string %1-%9
func (ms *matchState) matchCapture(s, p int) (int, bool) {
	n := ms.checkCapture(ms.pattern[p])
	cap := ms.captures[n]
	length := cap.end - cap.start
	if s+length > ms.srcEnd {
		return 0, false
	}
	if ms.src[s:s+length] != ms.src[cap.start:cap.end] {
		return 0, false
	}
	return s + length, true
}

// Match frontier pattern %f[set]
func (ms *matchState) matchFrontier(s, p int) (int, bool) {
	if p >= len(ms.pattern) || ms.pattern[p] != '[' {
		Errorf(ms.l, "missing '[' after '%%f' in pattern")
	}
	end := classEnd(ms.pattern, p)
	if end < 0 {
		Errorf(ms.l, "malformed pattern (missing ']')")
	}
	var prev byte = 0
	if s > 0 {
		prev = ms.src[s-1]
	}
	var curr byte = 0
	if s < ms.srcEnd {
		curr = ms.src[s]
	}
	if ms.matchBracketClass(prev, p, end) || !ms.matchBracketClass(curr, p, end) {
		return 0, false
	}
	return s, true // Return same position (frontier is zero-width)
}

// Match with max expansion (greedy)
func (ms *matchState) maxExpand(s, p, ep int) (int, bool) {
	i := 0
	for s+i < ms.srcEnd {
		matched, _ := ms.singleMatch(ms.src[s+i], p)
		if !matched {
			break
		}
		i++
	}
	// Try to match rest with maximum, then backtrack
	for i >= 0 {
		res, ok := ms.match(s+i, ep)
		if ok {
			return res, true
		}
		i--
	}
	return 0, false
}

// Match with min expansion (non-greedy)
func (ms *matchState) minExpand(s, p, ep int) (int, bool) {
	for {
		res, ok := ms.match(s, ep)
		if ok {
			return res, true
		}
		if s < ms.srcEnd {
			matched, _ := ms.singleMatch(ms.src[s], p)
			if matched {
				s++
				continue
			}
		}
		return 0, false
	}
}

// Main matching function
func (ms *matchState) match(s, p int) (int, bool) {
	ms.matchDepth++
	if ms.matchDepth > maxMatchDepth {
		Errorf(ms.l, "pattern too complex")
	}
	defer func() { ms.matchDepth-- }()

	for p < len(ms.pattern) {
		switch ms.pattern[p] {
		case '(':
			if p+1 < len(ms.pattern) && ms.pattern[p+1] == ')' {
				// Position capture: use -2 as marker
				return ms.startCapture(s, p+2, -2)
			}
			return ms.startCapture(s, p+1, -1) // -1 = unfinished
		case ')':
			return ms.endCapture(s, p+1)
		case '$':
			if p+1 == len(ms.pattern) {
				// End anchor
				if s == ms.srcEnd {
					return s, true
				}
				return 0, false
			}
			// $ not at end is literal
			goto dflt
		case '%':
			if p+1 >= len(ms.pattern) {
				Errorf(ms.l, "malformed pattern (ends with '%%')")
			}
			switch ms.pattern[p+1] {
			case 'b':
				newS, ok := ms.matchBalance(s, p+2)
				if !ok {
					return 0, false
				}
				s = newS
				p += 4
				continue
			case 'f':
				newS, ok := ms.matchFrontier(s, p+2)
				if !ok {
					return 0, false
				}
				s = newS
				end := classEnd(ms.pattern, p+2)
				if end < 0 {
					Errorf(ms.l, "malformed pattern (missing ']')")
				}
				p = end
				continue
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				newS, ok := ms.matchCapture(s, p+1)
				if !ok {
					return 0, false
				}
				s = newS
				p += 2
				continue
			default:
				goto dflt
			}
		default:
			goto dflt
		}
	dflt:
		// Find end of current pattern item
		ep := p
		switch ms.pattern[p] {
		case '%':
			ep = p + 2
		case '[':
			ep = classEnd(ms.pattern, p)
			if ep < 0 {
				Errorf(ms.l, "malformed pattern (missing ']')")
			}
		default:
			ep = p + 1
		}

		// Check for repetition
		if ep < len(ms.pattern) {
			switch ms.pattern[ep] {
			case '*':
				return ms.maxExpand(s, p, ep+1)
			case '+':
				// One or more
				if s < ms.srcEnd {
					matched, _ := ms.singleMatch(ms.src[s], p)
					if matched {
						return ms.maxExpand(s+1, p, ep+1)
					}
				}
				return 0, false
			case '-':
				return ms.minExpand(s, p, ep+1)
			case '?':
				// Zero or one
				if s < ms.srcEnd {
					matched, _ := ms.singleMatch(ms.src[s], p)
					if matched {
						res, ok := ms.match(s+1, ep+1)
						if ok {
							return res, true
						}
					}
				}
				return ms.match(s, ep+1)
			}
		}

		// No repetition, single match
		if s >= ms.srcEnd {
			return 0, false
		}
		matched, _ := ms.singleMatch(ms.src[s], p)
		if !matched {
			return 0, false
		}
		s++
		p = ep
	}
	return s, true
}

// Push capture results onto stack
func (ms *matchState) pushCaptures(sstart, send int) int {
	if ms.numCaptures == 0 {
		// No captures, push whole match
		ms.l.PushString(ms.src[sstart:send])
		return 1
	}
	for i := 0; i < ms.numCaptures; i++ {
		cap := ms.captures[i]
		if cap.end == -1 {
			Errorf(ms.l, "unfinished capture")
		}
		if cap.end == -2 {
			// Position capture: () returns position as integer
			ms.l.PushInteger(cap.start + 1) // 1-based position
		} else {
			ms.l.PushString(ms.src[cap.start:cap.end])
		}
	}
	return ms.numCaptures
}

// Push one capture for gsub
func (ms *matchState) pushOneCapture(i, sstart, send int) {
	if i >= ms.numCaptures {
		if i == 0 {
			ms.l.PushString(ms.src[sstart:send])
		} else {
			Errorf(ms.l, "invalid capture index %%%d", i+1)
		}
		return
	}
	cap := ms.captures[i]
	if cap.end == -1 {
		Errorf(ms.l, "unfinished capture")
	}
	if cap.end == -2 {
		// Position capture
		ms.l.PushInteger(cap.start + 1)
	} else {
		ms.l.PushString(ms.src[cap.start:cap.end])
	}
}

// Check if pattern has special characters
func noSpecials(pattern string) bool {
	return !strings.ContainsAny(pattern, patternSpecials)
}

func findHelper(l *State, isFind bool) int {
	s, p := CheckString(l, 1), CheckString(l, 2)
	init := relativePosition(OptInteger(l, 3, 1), len(s))
	if init < 1 {
		init = 1
	} else if init > len(s)+1 {
		l.PushNil()
		return 1
	}

	// For find with plain=true or no special characters, use simple search
	if isFind {
		isPlain := l.ToBoolean(4)
		if isPlain || noSpecials(p) {
			if start := strings.Index(s[init-1:], p); start >= 0 {
				l.PushInteger(start + init)
				l.PushInteger(start + init + len(p) - 1)
				return 2
			}
			l.PushNil()
			return 1
		}
	}

	// Pattern matching
	anchor := len(p) > 0 && p[0] == '^'
	patStart := 0
	if anchor {
		patStart = 1
	}

	ms := &matchState{
		l:       l,
		src:     s,
		srcEnd:  len(s),
		pattern: p[patStart:],
	}

	spos := init - 1 // Convert to 0-based
	for {
		ms.captures = ms.captures[:0]
		ms.numCaptures = 0
		ms.matchDepth = 0

		if end, ok := ms.match(spos, 0); ok {
			if isFind {
				l.PushInteger(spos + 1) // 1-based start
				l.PushInteger(end)      // 1-based end (end is already past-the-end in 0-based)
				return 2 + ms.pushCaptures(spos, end)
			}
			return ms.pushCaptures(spos, end)
		}

		spos++
		if spos > len(s) || anchor {
			break
		}
	}

	l.PushNil()
	return 1
}

func scanFormat(l *State, fs string) string {
	i := 0
	skipDigit := func() {
		if unicode.IsDigit(rune(fs[i])) {
			i++
		}
	}
	flags := "-+ #0"
	for i < len(fs) && strings.ContainsRune(flags, rune(fs[i])) {
		i++
	}
	if i >= len(flags) {
		Errorf(l, "invalid format (repeated flags)")
	}
	skipDigit()
	skipDigit()
	if fs[i] == '.' {
		i++
		skipDigit()
		skipDigit()
	}
	if unicode.IsDigit(rune(fs[i])) {
		Errorf(l, "invalid format (width or precision too long)")
	}
	i++
	return "%" + fs[:i]
}

func formatHelper(l *State, fs string, argCount int) string {
	var b bytes.Buffer
	for i, arg := 0, 1; i < len(fs); i++ {
		if fs[i] != '%' {
			b.WriteByte(fs[i])
		} else if i++; fs[i] == '%' {
			b.WriteByte(fs[i])
		} else {
			if arg++; arg > argCount {
				ArgumentError(l, arg, "no value")
			}
			f := scanFormat(l, fs[i:])
			switch i += len(f) - 2; fs[i] {
			case 'c':
				// Ensure each character is represented by a single byte, while preserving format modifiers.
				c := CheckInteger(l, arg)
				fmt.Fprintf(&b, f, 'x')
				buf := b.Bytes()
				buf[len(buf)-1] = byte(c)
			case 'i': // The fmt package doesn't support %i.
				f = f[:len(f)-1] + "d"
				fallthrough
			case 'd':
				// Lua 5.3: handle integers directly to preserve precision
				v := l.ToValue(arg)
				switch val := v.(type) {
				case int64:
					fmt.Fprintf(&b, f, val)
				case float64:
					ArgumentCheck(l, math.Floor(val) == val && -math.Pow(2, 63) <= val && val < math.Pow(2, 63), arg, "number has no integer representation")
					fmt.Fprintf(&b, f, int64(val))
				default:
					Errorf(l, "number expected")
				}
			case 'u': // The fmt package doesn't support %u.
				// Lua 5.3: handle integers as unsigned
				v := l.ToValue(arg)
				switch val := v.(type) {
				case int64:
					fmt.Fprintf(&b, "%d", uint64(val))
				case float64:
					ArgumentCheck(l, math.Floor(val) == val && 0.0 <= val && val < math.Pow(2, 64), arg, "not a non-negative number in proper range")
					fmt.Fprintf(&b, "%d", uint64(val))
				default:
					Errorf(l, "number expected")
				}
			case 'o', 'x', 'X':
				// Lua 5.3: integers (including negative) are treated as unsigned
				v := l.ToValue(arg)
				switch val := v.(type) {
				case int64:
					fmt.Fprintf(&b, f, uint64(val))
				case float64:
					ArgumentCheck(l, 0.0 <= val && val < math.Pow(2, 64), arg, "not a non-negative number in proper range")
					fmt.Fprintf(&b, f, uint64(val))
				default:
					Errorf(l, "number expected")
				}
			case 'e', 'E', 'f', 'g', 'G':
				fmt.Fprintf(&b, f, CheckNumber(l, arg))
			case 'a', 'A':
				// Lua 5.3: hexadecimal floating-point format
				// Go uses %x/%X for hex floats, Lua uses %a/%A
				n := CheckNumber(l, arg)
				if fs[i] == 'a' {
					f = f[:len(f)-1] + "x"
				} else {
					f = f[:len(f)-1] + "X"
				}
				s := fmt.Sprintf(f, n)
				// Normalize exponent: Go uses 2-digit exponent (P+00), Lua uses minimal (P+0)
				// Remove leading zeros from exponent
				for j := 0; j < len(s); j++ {
					if (s[j] == 'p' || s[j] == 'P') && j+2 < len(s) {
						// Found exponent, check for sign
						expStart := j + 1
						if s[expStart] == '+' || s[expStart] == '-' {
							expStart++
						}
						// Remove leading zeros from exponent (but keep at least one digit)
						expEnd := len(s)
						numStart := expStart
						for numStart < expEnd-1 && s[numStart] == '0' {
							numStart++
						}
						if numStart > expStart {
							s = s[:expStart] + s[numStart:]
						}
						break
					}
				}
				b.WriteString(s)
			case 'q':
				// Lua 5.3: %q handles multiple types
				switch v := l.ToValue(arg).(type) {
				case nil:
					b.WriteString("nil")
				case bool:
					if v {
						b.WriteString("true")
					} else {
						b.WriteString("false")
					}
				case int64:
					// For mininteger, use hex format since decimal would be parsed as float
					if v == math.MinInt64 {
						fmt.Fprintf(&b, "0x%x", uint64(v))
					} else {
						fmt.Fprintf(&b, "%d", v)
					}
				case float64:
					// Use hex float format for precise representation
					if math.IsInf(v, 0) || math.IsNaN(v) {
						// Special values can't be represented as literals
						if math.IsInf(v, 1) {
							b.WriteString("1e9999")
						} else if math.IsInf(v, -1) {
							b.WriteString("-1e9999")
						} else {
							b.WriteString("(0/0)")
						}
					} else {
						fmt.Fprintf(&b, "%x", v)
					}
				case string:
					b.WriteByte('"')
					for i := 0; i < len(v); i++ {
						switch v[i] {
						case '"', '\\', '\n':
							b.WriteByte('\\')
							b.WriteByte(v[i])
						default:
							if 0x20 <= v[i] && v[i] != 0x7f { // ASCII control characters don't correspond to a Unicode range.
								b.WriteByte(v[i])
							} else if i+1 < len(v) && unicode.IsDigit(rune(v[i+1])) {
								fmt.Fprintf(&b, "\\%03d", v[i])
							} else {
								fmt.Fprintf(&b, "\\%d", v[i])
							}
						}
					}
					b.WriteByte('"')
				default:
					Errorf(l, "no literal")
				}
			case 's':
				s, _ := ToStringMeta(l, arg)
				// Lua 5.3: %s with width/precision must error if string contains zeros
				hasWidthOrPrecision := len(f) > 2 // more than just "%s"
				if hasWidthOrPrecision && strings.ContainsRune(s, 0) {
					Errorf(l, "string contains zeros")
				}
				if !strings.ContainsRune(f, '.') && len(s) >= 100 {
					b.WriteString(s)
				} else {
					fmt.Fprintf(&b, f, s)
				}
			default:
				Errorf(l, fmt.Sprintf("invalid option '%%%c' to 'format'", fs[i]))
			}
		}
	}
	return b.String()
}

// Pack/Unpack support for Lua 5.3
// Format options:
//   < = little endian, > = big endian, = = native endian
//   ![n] = set max alignment to n (1-16, default native)
//   b/B = signed/unsigned byte
//   h/H = signed/unsigned short (2 bytes)
//   l/L = signed/unsigned long (4 bytes)
//   j/J = lua_Integer/lua_Unsigned (8 bytes)
//   T = size_t (8 bytes)
//   i[n]/I[n] = signed/unsigned int with n bytes (default 4)
//   f = float (4 bytes), d = double (8 bytes), n = lua_Number (8 bytes)
//   cn = fixed string of n bytes
//   z = zero-terminated string
//   s[n] = string with length prefix of n bytes (default 8)
//   x = one byte padding
//   Xop = align to option op (no data)
//   (space) = ignored

type packState struct {
	fmt           string
	pos           int
	littleEnd     bool
	maxAlign      int
	alignExplicit bool // true if ! was used explicitly
}

func newPackState(fmt string) *packState {
	return &packState{
		fmt:           fmt,
		pos:           0,
		littleEnd:     nativeEndian() == binary.LittleEndian,
		maxAlign:      1, // default is 1 (no alignment); ! option changes this
		alignExplicit: false,
	}
}

func nativeEndian() binary.ByteOrder {
	// Check native endianness using unsafe
	var x uint16 = 0x0102
	b := *(*[2]byte)(unsafe.Pointer(&x))
	if b[0] == 0x02 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func (ps *packState) byteOrder() binary.ByteOrder {
	if ps.littleEnd {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func (ps *packState) eof() bool {
	return ps.pos >= len(ps.fmt)
}

func (ps *packState) peek() byte {
	if ps.eof() {
		return 0
	}
	return ps.fmt[ps.pos]
}

func (ps *packState) next() byte {
	if ps.eof() {
		return 0
	}
	c := ps.fmt[ps.pos]
	ps.pos++
	return c
}

func (ps *packState) getNum(def int) int {
	if ps.eof() || !isDigit(ps.peek()) {
		return def
	}
	n := 0
	// Limit to prevent overflow: stop when n * 10 + 9 would overflow.
	// This matches Lua 5.3's behavior which leaves excess digits unconsumed,
	// causing them to be treated as invalid format options.
	// Lua uses INT_MAX (2^31-1) even on 64-bit systems.
	const maxSize = 0x7FFFFFFF // INT_MAX
	const limit = (maxSize - 9) / 10
	for !ps.eof() && isDigit(ps.peek()) && n <= limit {
		n = n*10 + int(ps.next()-'0')
	}
	return n
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func (ps *packState) optSize(def int) int {
	return ps.getNum(def)
}

func (ps *packState) align(size int) int {
	if size > ps.maxAlign {
		size = ps.maxAlign
	}
	return size
}

// isPowerOf2 returns true if n is a power of 2
func isPowerOf2(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

func addPadding(buf *bytes.Buffer, pos, align int) int {
	if align <= 1 {
		return 0
	}
	pad := (align - (pos % align)) % align
	for i := 0; i < pad; i++ {
		buf.WriteByte(0)
	}
	return pad
}

func stringPack(l *State) int {
	fmtStr := CheckString(l, 1)
	ps := newPackState(fmtStr)
	var buf bytes.Buffer
	arg := 2
	totalSize := 0

	for !ps.eof() {
		opt := ps.next()
		switch opt {
		case ' ': // ignored
			continue
		case '<':
			ps.littleEnd = true
		case '>':
			ps.littleEnd = false
		case '=':
			ps.littleEnd = nativeEndian() == binary.LittleEndian
		case '!':
			ps.maxAlign = ps.optSize(8)
			ps.alignExplicit = true
			if ps.maxAlign < 1 || ps.maxAlign > 16 {
				Errorf(l, "integral size (%d) out of limits [1,16]", ps.maxAlign)
			}
		case 'b': // signed byte
			n := CheckInteger(l, arg)
			arg++
			if n < -128 || n > 127 {
				ArgumentError(l, arg-1, "integer overflow")
			}
			buf.WriteByte(byte(int8(n)))
			totalSize++
		case 'B': // unsigned byte
			n := CheckInteger(l, arg)
			arg++
			if n < 0 || n > 255 {
				ArgumentError(l, arg-1, "unsigned overflow")
			}
			buf.WriteByte(byte(n))
			totalSize++
		case 'h': // signed short (2 bytes)
			n := CheckInteger(l, arg)
			arg++
			align := ps.align(2)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 2)
			ps.byteOrder().PutUint16(b, uint16(int16(n)))
			buf.Write(b)
			totalSize += 2
		case 'H': // unsigned short (2 bytes)
			n := CheckInteger(l, arg)
			arg++
			align := ps.align(2)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 2)
			ps.byteOrder().PutUint16(b, uint16(n))
			buf.Write(b)
			totalSize += 2
		case 'l': // signed long (4 bytes)
			n := CheckInteger(l, arg)
			arg++
			align := ps.align(4)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 4)
			ps.byteOrder().PutUint32(b, uint32(int32(n)))
			buf.Write(b)
			totalSize += 4
		case 'L': // unsigned long (4 bytes)
			n := CheckInteger(l, arg)
			arg++
			align := ps.align(4)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 4)
			ps.byteOrder().PutUint32(b, uint32(n))
			buf.Write(b)
			totalSize += 4
		case 'j': // lua_Integer (8 bytes signed)
			n, ok := l.ToInteger64(arg)
			if !ok {
				ArgumentError(l, arg, "integer expected")
			}
			arg++
			align := ps.align(8)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 8)
			ps.byteOrder().PutUint64(b, uint64(n))
			buf.Write(b)
			totalSize += 8
		case 'J': // lua_Unsigned (8 bytes unsigned)
			n, ok := l.ToInteger64(arg)
			if !ok {
				ArgumentError(l, arg, "integer expected")
			}
			arg++
			align := ps.align(8)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 8)
			ps.byteOrder().PutUint64(b, uint64(n))
			buf.Write(b)
			totalSize += 8
		case 'T': // size_t (8 bytes on 64-bit)
			n, ok := l.ToInteger64(arg)
			if !ok {
				ArgumentError(l, arg, "integer expected")
			}
			arg++
			if n < 0 {
				ArgumentError(l, arg-1, "value out of range")
			}
			align := ps.align(8)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 8)
			ps.byteOrder().PutUint64(b, uint64(n))
			buf.Write(b)
			totalSize += 8
		case 'i', 'I': // signed/unsigned int with optional size
			size := ps.optSize(4)
			if size < 1 || size > 16 {
				Errorf(l, "integral size (%d) out of limits [1,16]", size)
			}
			n, ok := l.ToInteger64(arg)
			if !ok {
				ArgumentError(l, arg, "integer expected")
			}
			arg++
			// Overflow check for sizes < 8 bytes
			if size < 8 {
				if opt == 'I' {
					// Unsigned: check [0, 2^(size*8)-1]
					maxVal := uint64(1) << uint(size*8)
					if n < 0 || uint64(n) >= maxVal {
						ArgumentError(l, arg-1, "unsigned overflow")
					}
				} else {
					// Signed: check [-2^(size*8-1), 2^(size*8-1)-1]
					lim := int64(1) << uint(size*8-1)
					if n < -lim || n >= lim {
						ArgumentError(l, arg-1, "integer overflow")
					}
				}
			}
			align := ps.align(size)
			if ps.alignExplicit && align > 1 && !isPowerOf2(align) {
				ArgumentError(l, 1, "format asks for alignment not power of 2")
			}
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 16)
			if opt == 'I' {
				// Unsigned: zero-extend
				if ps.littleEnd {
					binary.LittleEndian.PutUint64(b, uint64(n))
				} else {
					binary.BigEndian.PutUint64(b[8:], uint64(n))
				}
			} else {
				// Signed: sign-extend
				if ps.littleEnd {
					binary.LittleEndian.PutUint64(b, uint64(n))
					if n < 0 {
						for i := 8; i < 16; i++ {
							b[i] = 0xff
						}
					}
				} else {
					binary.BigEndian.PutUint64(b[8:], uint64(n))
					if n < 0 {
						for i := 0; i < 8; i++ {
							b[i] = 0xff
						}
					}
				}
			}
			if ps.littleEnd {
				buf.Write(b[:size])
			} else {
				buf.Write(b[16-size:])
			}
			totalSize += size
		case 'f': // float (4 bytes)
			n := CheckNumber(l, arg)
			arg++
			align := ps.align(4)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 4)
			ps.byteOrder().PutUint32(b, math.Float32bits(float32(n)))
			buf.Write(b)
			totalSize += 4
		case 'd', 'n': // double / lua_Number (8 bytes)
			n := CheckNumber(l, arg)
			arg++
			align := ps.align(8)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			b := make([]byte, 8)
			ps.byteOrder().PutUint64(b, math.Float64bits(n))
			buf.Write(b)
			totalSize += 8
		case 'c': // fixed string
			size := ps.getNum(-1)
			if size < 0 {
				Errorf(l, "missing size for format option 'c'")
			}
			s := CheckString(l, arg)
			arg++
			if len(s) > size {
				ArgumentError(l, arg-1, "string longer than given size")
			}
			if len(s) < size {
				buf.WriteString(s)
				for i := len(s); i < size; i++ {
					buf.WriteByte(0)
				}
			} else {
				buf.WriteString(s[:size])
			}
			totalSize += size
		case 'z': // zero-terminated string
			s := CheckString(l, arg)
			arg++
			// Check for embedded nulls
			if strings.ContainsRune(s, 0) {
				ArgumentError(l, arg-1, "string contains zeros")
			}
			buf.WriteString(s)
			buf.WriteByte(0)
			totalSize += len(s) + 1
		case 's': // string with length prefix
			size := ps.optSize(8)
			if size < 1 || size > 16 {
				Errorf(l, "integral size (%d) out of limits [1,16]", size)
			}
			s := CheckString(l, arg)
			arg++
			// Check if string length fits in size bytes
			if size < 8 {
				maxLen := uint64(1) << uint(size*8)
				if uint64(len(s)) >= maxLen {
					ArgumentError(l, arg-1, "string length does not fit in given size")
				}
			}
			align := ps.align(size)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
			// Write length (support up to 16 bytes)
			b := make([]byte, 16)
			if ps.littleEnd {
				binary.LittleEndian.PutUint64(b, uint64(len(s)))
				// Upper 8 bytes are 0 for small lengths
				buf.Write(b[:size])
			} else {
				binary.BigEndian.PutUint64(b[8:], uint64(len(s)))
				// Upper 8 bytes are 0 for small lengths
				buf.Write(b[16-size:])
			}
			totalSize += size
			// Write string data
			buf.WriteString(s)
			totalSize += len(s)
		case 'x': // one byte padding
			buf.WriteByte(0)
			totalSize++
		case 'X': // alignment only (no data read)
			if ps.eof() {
				Errorf(l, "invalid next option for option 'X'")
			}
			alignOpt := ps.next()
			alignSize := getOptionSizeForX(alignOpt, ps, l)
			align := ps.align(alignSize)
			pad := addPadding(&buf, totalSize, align)
			totalSize += pad
		default:
			Errorf(l, fmt.Sprintf("invalid format option '%c'", opt))
		}
	}

	l.PushString(buf.String())
	return 1
}

func getOptionSize(opt byte, ps *packState, l *State) int {
	switch opt {
	case 'b', 'B', 'x':
		return 1
	case 'h', 'H':
		return 2
	case 'l', 'L', 'f':
		return 4
	case 'j', 'J', 'T', 'd', 'n':
		return 8
	case 'i', 'I':
		size := ps.optSize(4)
		if size < 1 || size > 16 {
			Errorf(l, "integral size (%d) out of limits [1,16]", size)
		}
		return size
	case 's':
		size := ps.optSize(8)
		if size < 1 || size > 16 {
			Errorf(l, "integral size (%d) out of limits [1,16]", size)
		}
		return size
	default:
		return 1
	}
}

// getOptionSizeForX is like getOptionSize but errors on invalid options for X
func getOptionSizeForX(opt byte, ps *packState, l *State) int {
	switch opt {
	case 'b', 'B', 'x':
		return 1
	case 'h', 'H':
		return 2
	case 'l', 'L', 'f':
		return 4
	case 'j', 'J', 'T', 'd', 'n':
		return 8
	case 'i', 'I':
		size := ps.optSize(4)
		if size < 1 || size > 16 {
			Errorf(l, "integral size (%d) out of limits [1,16]", size)
		}
		return size
	case 's':
		size := ps.optSize(8)
		if size < 1 || size > 16 {
			Errorf(l, "integral size (%d) out of limits [1,16]", size)
		}
		return size
	default:
		// Invalid options for X: c, z, X, spaces, etc.
		Errorf(l, "invalid next option for option 'X'")
		return 1 // never reached
	}
}

func stringUnpack(l *State) int {
	fmtStr := CheckString(l, 1)
	data := CheckString(l, 2)
	pos := OptInteger(l, 3, 1)
	// Handle negative indices (count from end)
	if pos < 0 {
		pos = len(data) + pos + 1
	}
	if pos < 1 || pos > len(data)+1 {
		Errorf(l, "initial position out of string")
	}
	pos-- // Convert to 0-based

	ps := newPackState(fmtStr)
	results := 0

	for !ps.eof() {
		opt := ps.next()
		switch opt {
		case ' ':
			continue
		case '<':
			ps.littleEnd = true
		case '>':
			ps.littleEnd = false
		case '=':
			ps.littleEnd = nativeEndian() == binary.LittleEndian
		case '!':
			ps.maxAlign = ps.optSize(8)
		case 'b': // signed byte
			if pos >= len(data) {
				Errorf(l, "data string too short")
			}
			l.PushInteger(int(int8(data[pos])))
			pos++
			results++
		case 'B': // unsigned byte
			if pos >= len(data) {
				Errorf(l, "data string too short")
			}
			l.PushInteger(int(data[pos]))
			pos++
			results++
		case 'h': // signed short
			align := ps.align(2)
			pos = alignPos(pos, align)
			if pos+2 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint16([]byte(data[pos : pos+2]))
			l.PushInteger(int(int16(v)))
			pos += 2
			results++
		case 'H': // unsigned short
			align := ps.align(2)
			pos = alignPos(pos, align)
			if pos+2 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint16([]byte(data[pos : pos+2]))
			l.PushInteger(int(v))
			pos += 2
			results++
		case 'l': // signed long (4 bytes)
			align := ps.align(4)
			pos = alignPos(pos, align)
			if pos+4 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint32([]byte(data[pos : pos+4]))
			l.PushInteger(int(int32(v)))
			pos += 4
			results++
		case 'L': // unsigned long (4 bytes)
			align := ps.align(4)
			pos = alignPos(pos, align)
			if pos+4 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint32([]byte(data[pos : pos+4]))
			l.PushInteger64(int64(v))
			pos += 4
			results++
		case 'j': // lua_Integer (8 bytes signed)
			align := ps.align(8)
			pos = alignPos(pos, align)
			if pos+8 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint64([]byte(data[pos : pos+8]))
			l.PushInteger64(int64(v))
			pos += 8
			results++
		case 'J', 'T': // lua_Unsigned / size_t (8 bytes)
			align := ps.align(8)
			pos = alignPos(pos, align)
			if pos+8 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint64([]byte(data[pos : pos+8]))
			l.PushInteger64(int64(v))
			pos += 8
			results++
		case 'i': // signed int with optional size
			size := ps.optSize(4)
			if size < 1 || size > 16 {
				Errorf(l, "integral size (%d) out of limits [1,16]", size)
			}
			align := ps.align(size)
			pos = alignPos(pos, align)
			if pos+size > len(data) {
				Errorf(l, "data string too short")
			}
			var v int64
			if ps.littleEnd {
				b := make([]byte, 8)
				if size <= 8 {
					copy(b, data[pos:pos+size])
					// Sign extend
					if data[pos+size-1]&0x80 != 0 {
						for i := size; i < 8; i++ {
							b[i] = 0xff
						}
					}
				} else {
					// For sizes > 8, take lower 8 bytes
					copy(b, data[pos:pos+8])
					// Check upper bytes for proper sign extension
					signByte := byte(0)
					if b[7]&0x80 != 0 {
						signByte = 0xff
					}
					for i := 8; i < size; i++ {
						if data[pos+i] != signByte {
							Errorf(l, "%d-byte integer does not fit into Lua Integer", size)
						}
					}
				}
				v = int64(binary.LittleEndian.Uint64(b))
			} else {
				b := make([]byte, 8)
				if size <= 8 {
					copy(b[8-size:], data[pos:pos+size])
					// Sign extend
					if data[pos]&0x80 != 0 {
						for i := 0; i < 8-size; i++ {
							b[i] = 0xff
						}
					}
				} else {
					// For sizes > 8, take lower 8 bytes
					copy(b, data[pos+size-8:pos+size])
					// Check upper bytes for proper sign extension
					signByte := byte(0)
					if b[0]&0x80 != 0 {
						signByte = 0xff
					}
					for i := 0; i < size-8; i++ {
						if data[pos+i] != signByte {
							Errorf(l, "%d-byte integer does not fit into Lua Integer", size)
						}
					}
				}
				v = int64(binary.BigEndian.Uint64(b))
			}
			l.PushInteger64(v)
			pos += size
			results++
		case 'I': // unsigned int with optional size
			size := ps.optSize(4)
			if size < 1 || size > 16 {
				Errorf(l, "integral size (%d) out of limits [1,16]", size)
			}
			align := ps.align(size)
			pos = alignPos(pos, align)
			if pos+size > len(data) {
				Errorf(l, "data string too short")
			}
			var v uint64
			if ps.littleEnd {
				b := make([]byte, 8)
				if size <= 8 {
					copy(b, data[pos:pos+size])
				} else {
					// For sizes > 8, take lower 8 bytes
					copy(b, data[pos:pos+8])
					// Check upper bytes are zero
					for i := 8; i < size; i++ {
						if data[pos+i] != 0 {
							Errorf(l, "%d-byte integer does not fit into Lua Integer", size)
						}
					}
				}
				v = binary.LittleEndian.Uint64(b)
			} else {
				b := make([]byte, 8)
				if size <= 8 {
					copy(b[8-size:], data[pos:pos+size])
				} else {
					// For sizes > 8, take lower 8 bytes
					copy(b, data[pos+size-8:pos+size])
					// Check upper bytes are zero
					for i := 0; i < size-8; i++ {
						if data[pos+i] != 0 {
							Errorf(l, "%d-byte integer does not fit into Lua Integer", size)
						}
					}
				}
				v = binary.BigEndian.Uint64(b)
			}
			l.PushInteger64(int64(v))
			pos += size
			results++
		case 'f': // float (4 bytes)
			align := ps.align(4)
			pos = alignPos(pos, align)
			if pos+4 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint32([]byte(data[pos : pos+4]))
			l.PushNumber(float64(math.Float32frombits(v)))
			pos += 4
			results++
		case 'd', 'n': // double / lua_Number (8 bytes)
			align := ps.align(8)
			pos = alignPos(pos, align)
			if pos+8 > len(data) {
				Errorf(l, "data string too short")
			}
			v := ps.byteOrder().Uint64([]byte(data[pos : pos+8]))
			l.PushNumber(math.Float64frombits(v))
			pos += 8
			results++
		case 'c': // fixed string
			size := ps.getNum(-1)
			if size < 0 {
				Errorf(l, "missing size for format option 'c'")
			}
			if pos+size > len(data) {
				Errorf(l, "data string too short")
			}
			l.PushString(data[pos : pos+size])
			pos += size
			results++
		case 'z': // zero-terminated string
			end := pos
			for end < len(data) && data[end] != 0 {
				end++
			}
			if end >= len(data) {
				Errorf(l, "unfinished string for format 'z'")
			}
			l.PushString(data[pos:end])
			pos = end + 1
			results++
		case 's': // string with length prefix
			size := ps.optSize(8)
			if size < 1 || size > 16 {
				Errorf(l, "integral size (%d) out of limits [1,16]", size)
			}
			align := ps.align(size)
			pos = alignPos(pos, align)
			if pos+size > len(data) {
				Errorf(l, "data string too short")
			}
			// Read length (support up to 16 bytes)
			var strLen uint64
			if ps.littleEnd {
				b := make([]byte, 16)
				copy(b, data[pos:pos+size])
				strLen = binary.LittleEndian.Uint64(b)
			} else {
				b := make([]byte, 16)
				copy(b[16-size:], data[pos:pos+size])
				strLen = binary.BigEndian.Uint64(b[8:])
			}
			pos += size
			if pos+int(strLen) > len(data) {
				Errorf(l, "data string too short")
			}
			l.PushString(data[pos : pos+int(strLen)])
			pos += int(strLen)
			results++
		case 'x': // one byte padding
			if pos >= len(data) {
				Errorf(l, "data string too short")
			}
			pos++
		case 'X': // alignment only
			if ps.eof() {
				Errorf(l, "invalid next option for option 'X'")
			}
			alignOpt := ps.next()
			alignSize := getOptionSizeForX(alignOpt, ps, l)
			align := ps.align(alignSize)
			pos = alignPos(pos, align)
		default:
			Errorf(l, fmt.Sprintf("invalid format option '%c'", opt))
		}
	}

	// Push final position (1-based)
	l.PushInteger(pos + 1)
	return results + 1
}

func alignPos(pos, align int) int {
	if align <= 1 {
		return pos
	}
	return pos + (align-(pos%align))%align
}

func stringPacksize(l *State) int {
	fmtStr := CheckString(l, 1)
	ps := newPackState(fmtStr)
	totalSize := 0

	// Maximum size for pack format result (matches Lua's MAXSIZE = INT_MAX)
	// Lua uses INT_MAX (2^31-1) even on 64-bit systems
	const maxSize = 0x7FFFFFFF // 2147483647

	// Helper to add size with overflow check
	addSize := func(size int) {
		if totalSize > maxSize-size {
			Errorf(l, "format result too large")
		}
		totalSize += size
	}

	for !ps.eof() {
		opt := ps.next()
		switch opt {
		case ' ':
			continue
		case '<', '>', '=':
			// Endianness doesn't affect size
		case '!':
			ps.maxAlign = ps.optSize(8)
		case 'b', 'B':
			addSize(1)
		case 'h', 'H':
			align := ps.align(2)
			totalSize = alignPos(totalSize, align)
			addSize(2)
		case 'l', 'L', 'f':
			align := ps.align(4)
			totalSize = alignPos(totalSize, align)
			addSize(4)
		case 'j', 'J', 'T', 'd', 'n':
			align := ps.align(8)
			totalSize = alignPos(totalSize, align)
			addSize(8)
		case 'i', 'I':
			size := ps.optSize(4)
			if size < 1 || size > 16 {
				Errorf(l, "integral size (%d) out of limits [1,16]", size)
			}
			align := ps.align(size)
			totalSize = alignPos(totalSize, align)
			addSize(size)
		case 'c':
			size := ps.getNum(-1)
			if size < 0 {
				Errorf(l, "missing size for format option 'c'")
			}
			addSize(size)
		case 'x':
			addSize(1)
		case 'X':
			if ps.eof() {
				Errorf(l, "invalid next option for option 'X'")
			}
			alignOpt := ps.next()
			alignSize := getOptionSizeForX(alignOpt, ps, l)
			align := ps.align(alignSize)
			totalSize = alignPos(totalSize, align)
		case 'z', 's':
			Errorf(l, "variable-length format")
		default:
			Errorf(l, fmt.Sprintf("invalid format option '%c'", opt))
		}
	}

	l.PushInteger(totalSize)
	return 1
}

// string.match(s, pattern [, init])
func stringMatch(l *State) int {
	return findHelper(l, false)
}

// gmatchAux is the iterator function for gmatch
func gmatchAux(l *State) int {
	s, _ := l.ToString(UpValueIndex(1))
	p, _ := l.ToString(UpValueIndex(2))
	pos, _ := l.ToInteger(UpValueIndex(3))
	lastMatch, _ := l.ToInteger(UpValueIndex(4)) // Track last successful match end (Lua 5.3.3)

	if pos > len(s) {
		l.PushNil()
		return 1
	}

	anchor := len(p) > 0 && p[0] == '^'
	patStart := 0
	if anchor {
		patStart = 1
	}

	ms := &matchState{
		l:       l,
		src:     s,
		srcEnd:  len(s),
		pattern: p[patStart:],
	}

	spos := pos // 0-based
	for spos <= len(s) {
		ms.captures = ms.captures[:0]
		ms.numCaptures = 0
		ms.matchDepth = 0

		// Lua 5.3.3: reject match if it ends at same position as last match
		if end, ok := ms.match(spos, 0); ok && end != lastMatch {
			// Update position and lastMatch for next iteration
			l.PushInteger(end)
			l.Replace(UpValueIndex(3))
			l.PushInteger(end)
			l.Replace(UpValueIndex(4))

			return ms.pushCaptures(spos, end)
		}

		spos++
		if anchor {
			break
		}
	}

	l.PushNil()
	return 1
}

// string.gmatch(s, pattern)
func stringGmatch(l *State) int {
	CheckString(l, 1)
	CheckString(l, 2)
	l.SetTop(2)
	l.PushInteger(0)  // Initial position (0-based)
	l.PushInteger(-1) // lastMatch - initialized to -1 (Lua 5.3.3)
	l.PushGoClosure(gmatchAux, 4)
	return 1
}

// addReplace handles replacement for gsub
func addReplace(l *State, ms *matchState, b *bytes.Buffer, sstart, send int) {
	switch l.TypeOf(3) {
	case TypeString, TypeNumber:
		repl, _ := l.ToString(3)
		for i := 0; i < len(repl); i++ {
			if repl[i] != '%' {
				b.WriteByte(repl[i])
			} else {
				i++
				if i >= len(repl) {
					Errorf(l, "invalid use of '%%' in replacement string")
				}
				if repl[i] == '%' {
					b.WriteByte('%')
				} else if repl[i] == '0' {
					b.WriteString(ms.src[sstart:send])
				} else if repl[i] >= '1' && repl[i] <= '9' {
					ms.pushOneCapture(int(repl[i]-'1'), sstart, send)
					s, ok := l.ToString(-1)
					if !ok {
						Errorf(l, "invalid capture value, a %s", l.TypeOf(-1).String())
					}
					b.WriteString(s)
					l.Pop(1)
				} else {
					Errorf(l, "invalid use of '%%' in replacement string")
				}
			}
		}
	case TypeFunction:
		l.PushValue(3)
		n := ms.pushCaptures(sstart, send)
		l.Call(n, 1)
		if !l.IsNil(-1) {
			if s, ok := l.ToString(-1); ok {
				b.WriteString(s)
			} else {
				Errorf(l, "invalid replacement value (a %s)", l.TypeOf(-1).String())
			}
		} else {
			// nil or false means no replacement, use original
			b.WriteString(ms.src[sstart:send])
		}
		l.Pop(1)
	case TypeTable:
		ms.pushOneCapture(0, sstart, send)
		l.Table(3)
		if !l.IsNil(-1) && l.ToBoolean(-1) {
			// Not nil and not false
			if s, ok := l.ToString(-1); ok {
				b.WriteString(s)
			} else {
				Errorf(l, "invalid replacement value (a %s)", l.TypeOf(-1).String())
			}
		} else {
			// nil or false means no replacement, use original
			b.WriteString(ms.src[sstart:send])
		}
		l.Pop(1)
	default:
		ArgumentError(l, 3, "string/function/table expected")
	}
}

// string.gsub(s, pattern, repl [, n])
func stringGsub(l *State) int {
	s := CheckString(l, 1)
	p := CheckString(l, 2)
	// repl is at position 3, type checked in addReplace
	maxRepl := OptInteger(l, 4, len(s)+1)

	anchor := len(p) > 0 && p[0] == '^'
	patStart := 0
	if anchor {
		patStart = 1
	}

	ms := &matchState{
		l:       l,
		src:     s,
		srcEnd:  len(s),
		pattern: p[patStart:],
	}

	var b bytes.Buffer
	n := 0
	spos := 0
	lastMatch := -1 // Track where last successful substitution ended (Lua 5.3.3)

	for n < maxRepl {
		ms.captures = ms.captures[:0]
		ms.numCaptures = 0
		ms.matchDepth = 0

		end, ok := ms.match(spos, 0)
		// Lua 5.3.3: reject match if it ends at same position as last match
		// This prevents double-substitution at the same position
		if ok && end != lastMatch {
			// Add replacement
			addReplace(l, ms, &b, spos, end)
			n++
			spos = end
			lastMatch = end
		} else if spos < len(s) {
			// No match (or same-position match): copy one char and advance
			b.WriteByte(s[spos])
			spos++
		} else {
			break // End of subject
		}

		if anchor {
			break
		}
	}

	// Add remainder
	if spos <= len(s) {
		b.WriteString(s[spos:])
	}

	l.PushString(b.String())
	l.PushInteger(n)
	return 2
}

var stringLibrary = []RegistryFunction{
	{"byte", func(l *State) int {
		s := CheckString(l, 1)
		start := relativePosition(OptInteger(l, 2, 1), len(s))
		end := relativePosition(OptInteger(l, 3, start), len(s))
		if start < 1 {
			start = 1
		}
		if end > len(s) {
			end = len(s)
		}
		if start > end {
			return 0
		}
		n := end - start + 1
		if start+n <= end {
			Errorf(l, "string slice too long")
		}
		CheckStackWithMessage(l, n, "string slice too long")
		for _, c := range []byte(s[start-1 : end]) {
			l.PushInteger(int(c))
		}
		return n
	}},
	{"char", func(l *State) int {
		var b bytes.Buffer
		for i, n := 1, l.Top(); i <= n; i++ {
			c := CheckInteger(l, i)
			ArgumentCheck(l, int(byte(c)) == c, i, "value out of range")
			b.WriteByte(byte(c))
		}
		l.PushString(b.String())
		return 1
	}},
	// {"dump", ...},
	{"find", func(l *State) int { return findHelper(l, true) }},
	{"format", func(l *State) int {
		l.PushString(formatHelper(l, CheckString(l, 1), l.Top()))
		return 1
	}},
	{"gmatch", stringGmatch},
	{"gsub", stringGsub},
	{"len", func(l *State) int { l.PushInteger(len(CheckString(l, 1))); return 1 }},
	{"lower", func(l *State) int { l.PushString(strings.ToLower(CheckString(l, 1))); return 1 }},
	{"match", stringMatch},
	{"rep", func(l *State) int {
		s, n, sep := CheckString(l, 1), CheckInteger(l, 2), OptString(l, 3, "")
		if n <= 0 {
			l.PushString("")
		} else if len(s)+len(sep) < len(s) || len(s)+len(sep) >= maxStringSize/n {
			Errorf(l, "resulting string too large")
		} else if sep == "" {
			l.PushString(strings.Repeat(s, n))
		} else {
			var b bytes.Buffer
			b.Grow(n*len(s) + (n-1)*len(sep))
			b.WriteString(s)
			for ; n > 1; n-- {
				b.WriteString(sep)
				b.WriteString(s)
			}
			l.PushString(b.String())
		}
		return 1
	}},
	{"pack", stringPack},
	{"packsize", stringPacksize},
	{"reverse", func(l *State) int {
		s := CheckString(l, 1)
		b := []byte(s)
		for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
			b[i], b[j] = b[j], b[i]
		}
		l.PushString(string(b))
		return 1
	}},
	{"sub", func(l *State) int {
		s := CheckString(l, 1)
		start, end := relativePosition(CheckInteger(l, 2), len(s)), relativePosition(OptInteger(l, 3, -1), len(s))
		if start < 1 {
			start = 1
		}
		if end > len(s) {
			end = len(s)
		}
		if start <= end {
			l.PushString(s[start-1 : end])
		} else {
			l.PushString("")
		}
		return 1
	}},
	{"unpack", stringUnpack},
	{"upper", func(l *State) int { l.PushString(strings.ToUpper(CheckString(l, 1))); return 1 }},
}

// StringOpen opens the string library. Usually passed to Require.
func StringOpen(l *State) int {
	NewLibrary(l, stringLibrary)
	l.CreateTable(0, 1)
	l.PushString("")
	l.PushValue(-2)
	l.SetMetaTable(-2)
	l.Pop(1)
	l.PushValue(-2)
	l.SetField(-2, "__index")
	l.Pop(1)
	return 1
}
