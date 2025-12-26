package lua

import (
	"unicode/utf8"
)

// utf8Pattern matches exactly one UTF-8 byte sequence
// This is the Lua pattern: [\0-\x7F\xC2-\xF4][\x80-\xBF]*
const utf8Pattern = "[\x00-\x7F\xC2-\xF4][\x80-\xBF]*"

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
		buf := make([]byte, 0, n*4) // UTF-8 uses at most 4 bytes per character
		for i := 1; i <= n; i++ {
			code := CheckInteger(l, i)
			if code < 0 || code > 0x10FFFF {
				ArgumentError(l, i, "value out of range")
			}
			var tmp [4]byte
			size := utf8.EncodeRune(tmp[:], rune(code))
			buf = append(buf, tmp[:size]...)
		}
		l.PushString(string(buf))
		return 1
	}},

	// utf8.codes(s) - returns iterator function
	{"codes", func(l *State) int {
		CheckString(l, 1) // validate argument
		l.PushGoFunction(func(l *State) int {
			// Iterator: state is the string, control is the START position of previous char (or 0)
			str := CheckString(l, 1)
			prevPos := CheckInteger(l, 2)

			var nextPos int
			if prevPos == 0 {
				nextPos = 1 // start from beginning
			} else {
				// Find the end of the character at prevPos, then advance
				_, size, ok := decodeUTF8(str, prevPos)
				if !ok {
					Errorf(l, "invalid UTF-8 code at position %d", prevPos)
				}
				nextPos = prevPos + size
			}

			if nextPos > len(str) {
				return 0 // end of iteration
			}

			r, _, ok := decodeUTF8(str, nextPos)
			if !ok {
				Errorf(l, "invalid UTF-8 code at position %d", nextPos)
			}

			l.PushInteger(nextPos) // becomes new control
			l.PushInteger(int(r))
			return 2
		})
		l.PushValue(1)   // string as state
		l.PushInteger(0) // initial position
		return 3
	}},

	// utf8.codepoint(s [, i [, j]]) - returns codepoints
	{"codepoint", func(l *State) int {
		s := CheckString(l, 1)
		i := utf8PosRelative(OptInteger(l, 2, 1), len(s))
		j := utf8PosRelative(OptInteger(l, 3, i), len(s))

		// Empty range check first - if i > j, just return nothing
		if i > j {
			return 0
		}
		// Only check bounds when we actually have a range to process
		if i < 1 || i > len(s) {
			ArgumentError(l, 2, "out of range")
		}
		if j > len(s) {
			ArgumentError(l, 3, "out of range")
		}

		n := 0
		pos := i
		for pos <= j {
			r, size, ok := decodeUTF8(s, pos)
			if !ok {
				Errorf(l, "invalid UTF-8 code at position %d", pos)
			}
			l.PushInteger(int(r))
			n++
			pos += size
		}
		return n
	}},

	// utf8.len(s [, i [, j]]) - returns number of characters
	{"len", func(l *State) int {
		s := CheckString(l, 1)
		i := utf8PosRelative(OptInteger(l, 2, 1), len(s))
		j := utf8PosRelative(OptInteger(l, 3, len(s)), len(s))

		if i < 1 {
			i = 1
		}
		if j > len(s) {
			j = len(s)
		}
		if i > j {
			l.PushInteger(0)
			return 1
		}

		count := 0
		pos := i
		for pos <= j {
			r, size, ok := decodeUTF8(s, pos)
			if !ok || r == utf8.RuneError {
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
	{"offset", func(l *State) int {
		s := CheckString(l, 1)
		n := CheckInteger(l, 2)
		var i int
		if n >= 0 {
			i = OptInteger(l, 3, 1)
		} else {
			i = OptInteger(l, 3, len(s)+1)
		}

		if i < 1 || i > len(s)+1 {
			ArgumentError(l, 3, "position out of range")
		}

		// For n != 0, the initial position must not be a continuation byte
		if n != 0 && i <= len(s) && isContinuationByte(s[i-1]) {
			ArgumentError(l, 3, "initial position is a continuation byte")
		}

		if n == 0 {
			// Find the beginning of the character at position i
			// Note: i can be len(s)+1, so we must check i <= len(s) before accessing s[i-1]
			for i > 1 && i <= len(s) && isContinuationByte(s[i-1]) {
				i--
			}
			l.PushInteger(i)
			return 1
		}

		if n > 0 {
			// Move forward n characters from position i
			pos := i
			// First, make sure we're at the start of a character
			for pos <= len(s) && isContinuationByte(s[pos-1]) {
				pos++
			}
			n-- // We're at the first character already
			for n > 0 && pos <= len(s) {
				_, size, ok := decodeUTF8(s, pos)
				if !ok {
					l.PushNil()
					return 1
				}
				pos += size
				n--
			}
			if n == 0 && pos <= len(s)+1 {
				l.PushInteger(pos)
				return 1
			}
		} else {
			// Move backward -n characters from position i
			pos := i
			// Move to the start of the current character
			// Note: pos can be len(s)+1, so we must check pos <= len(s) before accessing s[pos-1]
			for pos > 1 && pos <= len(s) && isContinuationByte(s[pos-1]) {
				pos--
			}
			for n < 0 && pos > 1 {
				pos--
				for pos > 1 && isContinuationByte(s[pos-1]) {
					pos--
				}
				n++
			}
			if n == 0 {
				l.PushInteger(pos)
				return 1
			}
		}

		l.PushNil()
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
