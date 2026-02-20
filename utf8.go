package lua

import (
	"unicode/utf8"
)

// utf8Pattern matches exactly one UTF-8 byte sequence (including modified UTF-8)
// This is the Lua 5.4 pattern: [\0-\x7F\xC2-\xFD][\x80-\xBF]*
const utf8Pattern = "[\x00-\x7F\xC2-\xFD][\x80-\xBF]*"

// decodeUTF8 decodes a single UTF-8 character from s starting at byte position pos (1-based).
// Returns the rune, its size in bytes, and true if valid; otherwise returns 0, 0, false.
func decodeUTF8(s string, pos int) (rune, int, bool) {
	if pos < 1 || pos > len(s) {
		return 0, 0, false
	}
	r, size := utf8.DecodeRuneInString(s[pos-1:])
	if r == utf8.RuneError && size <= 1 {
		return 0, 0, false
	}
	return r, size, true
}

// decodeUTF8Lax decodes a single modified UTF-8 character (1-based pos).
// Accepts surrogates (U+D800..U+DFFF) and codepoints up to U+7FFFFFFF.
func decodeUTF8Lax(s string, pos int) (rune, int, bool) {
	if pos < 1 || pos > len(s) {
		return 0, 0, false
	}
	b := s[pos-1:]
	first := b[0]
	switch {
	case first < 0x80:
		return rune(first), 1, true
	case first < 0xC0:
		return 0, 0, false // continuation byte
	case first < 0xE0:
		if len(b) < 2 || b[1]&0xC0 != 0x80 {
			return 0, 0, false
		}
		r := rune(first&0x1F)<<6 | rune(b[1]&0x3F)
		return r, 2, true
	case first < 0xF0:
		if len(b) < 3 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 {
			return 0, 0, false
		}
		r := rune(first&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
		return r, 3, true
	case first < 0xF8:
		if len(b) < 4 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 {
			return 0, 0, false
		}
		r := rune(first&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
		return r, 4, true
	case first < 0xFC:
		if len(b) < 5 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 || b[4]&0xC0 != 0x80 {
			return 0, 0, false
		}
		r := rune(first&0x03)<<24 | rune(b[1]&0x3F)<<18 | rune(b[2]&0x3F)<<12 | rune(b[3]&0x3F)<<6 | rune(b[4]&0x3F)
		return r, 5, true
	case first < 0xFE:
		if len(b) < 6 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 || b[4]&0xC0 != 0x80 || b[5]&0xC0 != 0x80 {
			return 0, 0, false
		}
		r := rune(first&0x01)<<30 | rune(b[1]&0x3F)<<24 | rune(b[2]&0x3F)<<18 | rune(b[3]&0x3F)<<12 | rune(b[4]&0x3F)<<6 | rune(b[5]&0x3F)
		return r, 6, true
	default:
		return 0, 0, false
	}
}

// utf8PosRelative converts a potentially negative position to a positive one.
// Negative positions count from the end of the string.
func utf8PosRelative(pos, len int) int {
	if pos >= 0 {
		return pos
	}
	if -pos > len {
		return 0
	}
	return len + pos + 1
}

var utf8Library = []RegistryFunction{
	// utf8.char(...) - converts codepoints to UTF-8 string
	{"char", func(l *State) int {
		n := l.Top()
		buf := make([]byte, 0, n*4)
		for i := 1; i <= n; i++ {
			code := CheckInteger(l, i)
			if code < 0 || code > 0x7FFFFFFF {
				ArgumentError(l, i, "value out of range")
			}
			var tmp [8]byte
			size := encodeUTF8(tmp[:], uint64(code))
			buf = append(buf, tmp[:size]...)
		}
		l.PushString(string(buf))
		return 1
	}},

	// utf8.codes(s [, lax]) - returns iterator function, string, and 0
	// The iterator uses the same position scheme as C Lua 5.4.8:
	// control variable is the 1-based position of the last decoded char.
	// On each call, skip continuation bytes at that position to find the next char.
	{"codes", func(l *State) int {
		s := CheckString(l, 1)
		lax := l.ToBoolean(2)
		// Check that string starts with a valid UTF-8 byte (not a continuation byte)
		if !lax && len(s) > 0 && s[0]&0xC0 == 0x80 {
			ArgumentError(l, 1, "invalid UTF-8 code")
		}
		// Capture lax in closure via upvalue
		isLax := lax
		l.PushGoFunction(func(l *State) int {
			str := CheckString(l, 1)
			// n is the raw control value; cast to uint64 so negatives wrap to large values
			nraw, _ := l.ToInteger64(2)
			n := uint64(nraw)
			slen := uint64(len(str))
			// Skip continuation bytes at position n
			if n < slen {
				for n < slen && str[n]&0xC0 == 0x80 {
					n++
				}
			}
			if n >= slen {
				return 0 // no more codepoints
			}
			// Decode UTF-8 at position n (0-based index)
			if isLax {
				r, size, ok := decodeUTF8Lax(str, int(n)+1) // 1-based for decodeUTF8Lax
				if !ok {
					Errorf(l, "invalid UTF-8 code")
				}
				l.PushInteger(int(n) + 1) // 1-based position
				l.PushInteger(int(r))     // codepoint
				_ = size
				return 2
			}
			r, size := utf8.DecodeRuneInString(str[n:])
			if r == utf8.RuneError && size <= 1 {
				Errorf(l, "invalid UTF-8 code")
			}
			// Check that next byte after this char is not an orphan continuation
			if n+uint64(size) < slen && str[n+uint64(size)]&0xC0 == 0x80 {
				Errorf(l, "invalid UTF-8 code")
			}
			l.PushInteger(int(n) + 1) // 1-based position (also becomes control variable)
			l.PushInteger(int(r))     // codepoint
			return 2
		})
		l.PushValue(1)   // string as state
		l.PushInteger(0) // initial position (0 = before first char)
		return 3
	}},

	// utf8.codepoint(s [, i [, j [, lax]]]) - returns codepoints
	{"codepoint", func(l *State) int {
		s := CheckString(l, 1)
		i := utf8PosRelative(OptInteger(l, 2, 1), len(s))
		j := utf8PosRelative(OptInteger(l, 3, i), len(s))
		lax := l.ToBoolean(4)

		// Empty range check first - if i > j, just return nothing
		if i > j {
			return 0
		}
		// Only check bounds when we actually have a range to process
		if i < 1 || i > len(s) {
			ArgumentError(l, 2, "out of bounds")
		}
		if j > len(s) {
			ArgumentError(l, 3, "out of bounds")
		}

		decode := decodeUTF8
		if lax {
			decode = decodeUTF8Lax
		}

		n := 0
		pos := i
		for pos <= j {
			r, size, ok := decode(s, pos)
			if !ok {
				Errorf(l, "invalid UTF-8 code at position %d", pos)
			}
			l.PushInteger(int(r))
			n++
			pos += size
		}
		return n
	}},

	// utf8.len(s [, i [, j [, lax]]]) - returns number of characters
	{"len", func(l *State) int {
		s := CheckString(l, 1)
		i := utf8PosRelative(OptInteger(l, 2, 1), len(s))
		j := utf8PosRelative(OptInteger(l, 3, -1), len(s))
		lax := l.ToBoolean(4)

		ArgumentCheck(l, 1 <= i && i <= len(s)+1, 2, "initial position out of bounds")
		ArgumentCheck(l, j <= len(s), 3, "final position out of bounds")
		if i > j {
			l.PushInteger(0)
			return 1
		}

		decode := decodeUTF8
		if lax {
			decode = decodeUTF8Lax
		}

		count := 0
		pos := i
		for pos <= j {
			r, size, ok := decode(s, pos)
			if !ok || (!lax && r == utf8.RuneError) {
				// Return nil and the position of the invalid byte
				l.PushNil()
				l.PushInteger(pos)
				return 2
			}
			count++
			pos += size
		}
		l.PushInteger(count)
		return 1
	}},

	// utf8.offset(s, n [, i]) - returns byte position of n-th character
	// Like C Lua, navigates by continuation bytes without decoding.
	{"offset", func(l *State) int {
		s := CheckString(l, 1)
		n := CheckInteger(l, 2)
		var posi int
		if n >= 0 {
			posi = OptInteger(l, 3, 1)
		} else {
			posi = OptInteger(l, 3, len(s)+1)
		}

		ArgumentCheck(l, 1 <= posi && posi <= len(s)+1, 3, "position out of bounds")

		if n == 0 {
			// Find beginning of current byte sequence
			for posi > 1 && posi <= len(s) && isContinuationByte(s[posi-1]) {
				posi--
			}
		} else {
			if posi <= len(s) && isContinuationByte(s[posi-1]) {
				Errorf(l, "initial position is a continuation byte")
			}
			if n < 0 {
				for n < 0 && posi > 1 {
					// Find beginning of previous character
					posi--
					for posi > 1 && isContinuationByte(s[posi-1]) {
						posi--
					}
					n++
				}
			} else {
				n-- // Don't count character at 'posi'
				for n > 0 && posi <= len(s) {
					// Find beginning of next character
					posi++
					for posi <= len(s) && isContinuationByte(s[posi-1]) {
						posi++
					}
					n--
				}
			}
		}
		if n == 0 {
			l.PushInteger(posi)
		} else {
			l.PushNil()
		}
		return 1
	}},
}

// isContinuationByte returns true if b is a UTF-8 continuation byte (10xxxxxx)
func isContinuationByte(b byte) bool {
	return b&0xC0 == 0x80
}

// UTF8Open opens the utf8 library. Usually passed to Require.
func UTF8Open(l *State) int {
	NewLibrary(l, utf8Library)
	// Add charpattern
	l.PushString(utf8Pattern)
	l.SetField(-2, "charpattern")
	return 1
}
