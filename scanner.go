package lua

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const firstReserved = 257
const endOfStream = -1
const maxInt = int(^uint(0) >> 1)

const (
	tkAnd = iota + firstReserved
	tkBreak
	tkDo
	tkElse
	tkElseif
	tkEnd
	tkFalse
	tkFor
	tkFunction
	tkGoto
	tkIf
	tkIn
	tkLocal
	tkNil
	tkNot
	tkOr
	tkRepeat
	tkReturn
	tkThen
	tkTrue
	tkUntil
	tkWhile
	tkConcat
	tkDots
	tkEq
	tkGE
	tkLE
	tkNE
	tkDoubleColon
	tkIDiv // Lua 5.3: //
	tkShl  // Lua 5.3: <<
	tkShr  // Lua 5.3: >>
	tkEOS
	tkNumber
	tkInteger // Lua 5.3: integer literal
	tkName
	tkString
	reservedCount = tkWhile - firstReserved + 1
)

var tokens []string = []string{
	"and", "break", "do", "else", "elseif",
	"end", "false", "for", "function", "goto", "if",
	"in", "local", "nil", "not", "or", "repeat",
	"return", "then", "true", "until", "while",
	"..", "...", "==", ">=", "<=", "~=", "::",
	"//", "<<", ">>", // Lua 5.3 operators
	"<eof>",
	"<number>", "<integer>", "<name>", "<string>",
}

type token struct {
	t   rune
	n   float64
	i   int64  // Lua 5.3: integer value
	s   string
	raw string // original source text for error messages (txtToken)
}

type scanner struct {
	l                    *State
	buffer               bytes.Buffer
	r                    io.ByteReader
	current              rune
	lineNumber, lastLine int
	source               string
	lookAheadToken       token
	tokenBuf             string // last token's buffer content for error messages
	token
}

func (s *scanner) assert(cond bool)           { s.l.assert(cond) }
func (s *scanner) syntaxError(message string) { s.scanError(message, s.t) }
func (s *scanner) errorExpected(t rune)       { s.syntaxError(s.tokenToString(t) + " expected") }
func (s *scanner) numberError()               { s.scanError("malformed number", tkNumber) }
func isNewLine(c rune) bool { return c == '\n' || c == '\r' }
func isDecimal(c rune) bool { return '0' <= c && c <= '9' }
func isAlpha(c rune) bool   { return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') }

func (s *scanner) tokenToString(t rune) string {
	switch {
	case t < firstReserved:
		if t >= ' ' && t <= '~' { // printable ASCII character
			return fmt.Sprintf("'%c'", t)
		}
		return fmt.Sprintf("'<\\%d>'", t)
	case t < tkEOS:
		return fmt.Sprintf("'%s'", tokens[t-firstReserved])
	}
	return tokens[t-firstReserved]
}

func (s *scanner) txtToken(token rune) string {
	switch token {
	case tkName, tkString, tkNumber, tkInteger:
		// During scanning, the buffer may contain partial token text (e.g. escape errors).
		// After scanning, tokenBuf holds the raw text from the completed token.
		if s.buffer.Len() > 0 {
			return fmt.Sprintf("'%s'", s.buffer.String())
		}
		if s.tokenBuf != "" {
			return fmt.Sprintf("'%s'", s.tokenBuf)
		}
		return fmt.Sprintf("'%s'", s.s)
	default:
		return s.tokenToString(token)
	}
}

func (s *scanner) scanError(message string, token rune) {
	buff := chunkID(s.source)
	if token != 0 {
		message = fmt.Sprintf("%s:%d: %s near %s", buff, s.lineNumber, message, s.txtToken(token))
	} else {
		message = fmt.Sprintf("%s:%d: %s", buff, s.lineNumber, message)
	}
	s.l.push(message)
	s.l.throw(SyntaxError)
}

func (s *scanner) incrementLineNumber() {
	old := s.current
	s.assert(isNewLine(old))
	if s.advance(); isNewLine(s.current) && s.current != old {
		s.advance()
	}
	if s.lineNumber++; s.lineNumber >= maxInt {
		s.syntaxError("chunk has too many lines")
	}
}

func (s *scanner) advance() {
	if c, err := s.r.ReadByte(); err != nil {
		s.current = endOfStream
	} else {
		s.current = rune(c)
	}
}

func (s *scanner) saveAndAdvance() {
	s.save(s.current)
	s.advance()
}

func (s *scanner) advanceAndSave(c rune) {
	s.advance()
	s.save(c)
}

func (s *scanner) save(c rune) {
	if err := s.buffer.WriteByte(byte(c)); err != nil {
		s.scanError("lexical element too long", 0)
	}
}

func (s *scanner) checkNext(str string) bool {
	if s.current == 0 || !strings.ContainsRune(str, s.current) {
		return false
	}
	s.saveAndAdvance()
	return true
}

func (s *scanner) skipSeparator() int { // TODO is this the right name?
	i, c := 0, s.current
	s.assert(c == '[' || c == ']')
	for s.saveAndAdvance(); s.current == '='; i++ {
		s.saveAndAdvance()
	}
	if s.current == c {
		return i
	}
	return -i - 1
}

func (s *scanner) readMultiLine(comment bool, sep int) (str string, raw string) {
	if s.saveAndAdvance(); isNewLine(s.current) {
		s.incrementLineNumber()
	}
	for {
		switch s.current {
		case endOfStream:
			if comment {
				s.scanError("unfinished long comment", tkEOS)
			} else {
				s.scanError("unfinished long string", tkEOS)
			}
		case ']':
			if s.skipSeparator() == sep {
				s.saveAndAdvance()
				if !comment {
					raw = s.buffer.String()
					str = raw[2+sep : len(raw)-(2+sep)]
				}
				s.buffer.Reset()
				return
			}
		case '\r', '\n':
			s.save('\n')
			s.incrementLineNumber()
		default:
			if !comment {
				s.save(s.current)
			}
			s.advance()
		}
	}
}

func (s *scanner) readDigits() (c rune) {
	for c = s.current; isDecimal(c); c = s.current {
		s.saveAndAdvance()
	}
	return
}

func isHexadecimal(c rune) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func (s *scanner) readHexNumber(x float64) (n float64, c rune, i int, overflow int) {
	if c, n = s.current, x; !isHexadecimal(c) {
		return
	}
	// float64 can represent integers up to 2^53 precisely.
	// After that, we just count digits as exponent overflow.
	const maxPrecise = float64(1 << 53)
	for {
		origC := c // Save original character before conversion
		var digit float64
		switch {
		case '0' <= c && c <= '9':
			digit = float64(c - '0')
		case 'a' <= c && c <= 'f':
			digit = float64(c - 'a' + 10)
		case 'A' <= c && c <= 'F':
			digit = float64(c - 'A' + 10)
		default:
			return
		}
		s.save(origC) // Save hex digit for integer parsing
		s.advance()
		i++
		c = s.current
		if n >= maxPrecise {
			// Beyond float64 precision, just track overflow
			overflow++
		} else {
			n = n*16.0 + digit
		}
	}
}

// readHexFraction reads hex digits after the decimal point, returning the
// fractional value, current char, digit count, and exponent adjustment.
// It handles cases with many leading zeros by tracking them as exponent offset,
// and cases with many trailing zeros by dividing instead of multiplying.
func (s *scanner) readHexFraction() (frac float64, c rune, count int, expAdj int) {
	c = s.current
	leadingZeros := 0
	gotSignificant := false
	const maxPrecise = float64(1 << 53)

	for isHexadecimal(c) {
		origC := c
		var digit float64
		switch {
		case '0' <= c && c <= '9':
			digit = float64(c - '0')
		case 'a' <= c && c <= 'f':
			digit = float64(c - 'a' + 10)
		case 'A' <= c && c <= 'F':
			digit = float64(c - 'A' + 10)
		}
		s.save(origC)
		s.advance()
		count++
		c = s.current

		if !gotSignificant {
			if digit == 0 {
				// Track leading zeros for exponent adjustment
				leadingZeros++
				continue
			}
			gotSignificant = true
		}

		// Accumulate as integer-like value (we'll adjust with exponent)
		if frac < maxPrecise {
			frac = frac*16.0 + digit
		}
		// Digits beyond precision are ignored (they don't affect float64 result)
	}
	// The fractional value should be: frac / 16^(count)
	// But we return frac as accumulated value, with expAdj = -(leadingZeros + digits_accumulated) * 4
	// Actually simpler: expAdj tells us how many positions to shift
	// frac * 2^expAdj gives the correct fractional value
	if gotSignificant {
		digitsAccumulated := count - leadingZeros
		expAdj = -(leadingZeros + digitsAccumulated) * 4
	}
	return
}

func (s *scanner) readNumber() token {
	const bits64, base10, base16 = 64, 10, 16
	c := s.current
	s.assert(isDecimal(c))
	s.saveAndAdvance()
	if c == '0' && s.checkNext("Xx") { // hexadecimal
		prefix := s.buffer.String()
		s.assert(prefix == "0x" || prefix == "0X")
		s.buffer.Reset()
		var exponent int
		isFloat := false
		fraction, c, i, overflow := s.readHexNumber(0)
		var fracDigits int
		var fracExp int
		var frac float64
		if c == '.' {
			isFloat = true
			s.advance()
			frac, c, fracDigits, fracExp = s.readHexFraction()
		}
		if i == 0 && fracDigits == 0 {
			s.numberError()
		}
		// Each overflow digit = factor of 16 = 2^4
		exponent = overflow * 4
		// Combine integer and fractional parts
		// fraction * 2^exponent + frac * 2^fracExp
		if frac != 0 {
			if fraction == 0 {
				// Pure fractional number like 0x.ABC
				fraction = frac
				exponent = fracExp
			} else {
				// Mixed number like 0x3.14
				// fraction is the integer part, frac is accumulated fractional digits
				// fracExp = -(totalFracDigits) * 4
				// We need: fraction + frac * 2^fracExp
				// = fraction + frac / 16^totalFracDigits
				fraction = fraction + math.Ldexp(frac, fracExp)
			}
		}
		if c == 'p' || c == 'P' {
			isFloat = true
			s.buffer.Reset() // Clear buffer before reading exponent
			s.advance()
			var negativeExponent bool
			if c = s.current; c == '+' || c == '-' {
				negativeExponent = c == '-'
				s.advance()
			}
			if !isDecimal(s.current) {
				s.numberError()
			}
			_ = s.readDigits()
			if e, err := strconv.ParseInt(s.buffer.String(), base10, bits64); err != nil {
				s.numberError()
			} else if negativeExponent {
				exponent += int(-e)
			} else {
				exponent += int(e)
			}
			s.buffer.Reset()
		}
		// Lua 5.4: trailing alpha or underscore after hex number is malformed
		if isAlpha(s.current) || s.current == '_' {
			s.numberError()
		}
		// Lua 5.3: hex integer if no decimal point or 'p' exponent
		// Note: We check !isFloat, not exponent==0, because overflow tracking
		// may set exponent for float calculations, but integers use wrapping uint64
		if !isFloat {
			hexStr := s.buffer.String()
			s.buffer.Reset()
			// Parse as unsigned with wrapping for values larger than 64 bits
			// This matches Lua 5.3's behavior where overflow wraps around
			var uintVal uint64
			for _, c := range hexStr {
				var digit uint64
				switch {
				case '0' <= c && c <= '9':
					digit = uint64(c - '0')
				case 'a' <= c && c <= 'f':
					digit = uint64(c - 'a' + 10)
				case 'A' <= c && c <= 'F':
					digit = uint64(c - 'A' + 10)
				}
				uintVal = uintVal*16 + digit // naturally wraps on overflow
			}
			return token{t: tkInteger, i: int64(uintVal)}
		}
		s.buffer.Reset() // Clear buffer before returning hex float (e.g., 0x7.4)
		return token{t: tkNumber, n: math.Ldexp(fraction, exponent)}
	}
	// Decimal number
	isFloat := false
	c = s.readDigits()
	if c == '.' {
		isFloat = true
		s.saveAndAdvance()
		c = s.readDigits()
	}
	if c == 'e' || c == 'E' {
		isFloat = true
		s.saveAndAdvance()
		if c = s.current; c == '+' || c == '-' {
			s.saveAndAdvance()
		}
		_ = s.readDigits()
	}
	// Lua 5.4: trailing alpha or underscore after number is malformed
	if isAlpha(s.current) || s.current == '_' {
		s.saveAndAdvance()
	}
	str := s.buffer.String()
	if strings.HasPrefix(str, "0") {
		if str = strings.TrimLeft(str, "0"); str == "" || !isDecimal(rune(str[0])) {
			str = "0" + str
		}
	}
	s.buffer.Reset()
	// Lua 5.3: try to parse as integer if no decimal point or exponent
	if !isFloat {
		if intVal, err := strconv.ParseInt(str, base10, bits64); err == nil {
			return token{t: tkInteger, i: intVal, raw: str}
		}
		// Too large for int64, fall through to float
	}
	f, err := strconv.ParseFloat(str, bits64)
	if err != nil {
		// Accept overflow to +/-Inf (e.g., 1e9999) like C Lua does
		if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
			return token{t: tkNumber, n: f, raw: str}
		}
		s.numberError()
	}
	return token{t: tkNumber, n: f, raw: str}
}

var escapes map[rune]rune = map[rune]rune{
	'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '"': '"', '\'': '\'',
}

func (s *scanner) escapeError(c []rune, message string) {
	s.save('\\')
	for _, r := range c {
		if r == endOfStream {
			break
		}
		s.save(r)
	}
	s.scanError(message, tkString)
}

func (s *scanner) readHexEscape() (r rune) {
	s.advance()
	for i, c, b := 1, s.current, [3]rune{'x'}; i < len(b); i, c, r = i+1, s.current, r<<4+c {
		switch b[i] = c; {
		case '0' <= c && c <= '9':
			c = c - '0'
		case 'a' <= c && c <= 'f':
			c = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			c = c - 'A' + 10
		default:
			s.escapeError(b[:i+1], "hexadecimal digit expected")
		}
		s.advance()
	}
	return
}

func (s *scanner) readDecimalEscape() (r rune) {
	b := [4]rune{}
	i := 0
	for c := s.current; i < 3 && isDecimal(c); i, c = i+1, s.current {
		b[i], r = c, 10*r+c-'0'
		s.advance()
	}
	if r > math.MaxUint8 {
		b[i] = s.current
		s.escapeError(b[:i+1], "decimal escape too large")
	}
	return
}

// readUnicodeEscape reads a \u{xxxx} Unicode escape sequence (Lua 5.3/5.4).
// Returns the UTF-8 encoding of the codepoint.
// Lua 5.4 allows codepoints up to 0x7FFFFFFF (not just 0x10FFFF).
func (s *scanner) readUnicodeEscape() string {
	s.advance() // skip 'u'
	if s.current != '{' {
		s.escapeError([]rune{'u', s.current}, "missing '{'")
	}
	s.advance() // skip '{'

	var codepoint uint64
	var digits []rune // track digits for error messages
	digitCount := 0
	for {
		c := s.current
		if c == '}' {
			break
		}
		var digit uint64
		switch {
		case '0' <= c && c <= '9':
			digit = uint64(c - '0')
		case 'a' <= c && c <= 'f':
			digit = uint64(c-'a') + 10
		case 'A' <= c && c <= 'F':
			digit = uint64(c-'A') + 10
		default:
			seq := append([]rune{'u', '{'}, digits...)
			seq = append(seq, c)
			s.escapeError(seq, "hexadecimal digit expected")
		}
		digits = append(digits, c)
		codepoint = codepoint*16 + digit
		digitCount++
		if codepoint > 0x7FFFFFFF {
			seq := append([]rune{'u', '{'}, digits...)
			s.escapeError(seq, "UTF-8 value too large")
		}
		s.advance()
	}
	if digitCount == 0 {
		s.escapeError([]rune{'u', '{'}, "hexadecimal digit expected")
	}
	s.advance() // skip '}'

	// Encode codepoint as modified UTF-8 (up to 6 bytes for Lua 5.4)
	buf := make([]byte, 8)
	n := encodeUTF8(buf, codepoint)
	return string(buf[:n])
}

// encodeUTF8 encodes a codepoint as modified UTF-8 into buf.
// Supports codepoints up to 0x7FFFFFFF (Lua 5.4 extended range).
// Returns the number of bytes written.
func encodeUTF8(buf []byte, x uint64) int {
	if x < 0x80 {
		buf[0] = byte(x)
		return 1
	}
	// Use the same algorithm as C Lua's luaO_utf8esc:
	// Fill continuation bytes from the end, then add the lead byte.
	n := 1
	mfb := uint64(0x3f) // maximum that fits in first byte
	for {
		buf[8-n] = byte(0x80 | (x & 0x3f))
		n++
		x >>= 6
		mfb >>= 1
		if x <= mfb {
			break
		}
	}
	buf[8-n] = byte((^mfb << 1) | x)
	// Copy to front of buffer
	copy(buf[0:], buf[8-n:8])
	return n
}

func (s *scanner) readString() token {
	delimiter := s.current
	for s.saveAndAdvance(); s.current != delimiter; {
		switch s.current {
		case endOfStream:
			s.scanError("unfinished string", tkEOS)
		case '\n', '\r':
			s.scanError("unfinished string", tkString)
		case '\\':
			s.advance()
			c := s.current
			switch esc, ok := escapes[c]; {
			case ok:
				s.advanceAndSave(esc)
			case isNewLine(c):
				s.incrementLineNumber()
				s.save('\n')
			case c == endOfStream: // do nothing
			case c == 'x':
				s.save(s.readHexEscape())
			case c == 'u':
				// Lua 5.3 Unicode escape \u{xxxx}
				// Must iterate over bytes, not runes (range gives runes)
				str := s.readUnicodeEscape()
				for i := 0; i < len(str); i++ {
					s.save(rune(str[i]))
				}
			case c == 'z':
				for s.advance(); unicode.IsSpace(s.current); {
					if isNewLine(s.current) {
						s.incrementLineNumber()
					} else {
						s.advance()
					}
				}
			default:
				if !isDecimal(c) {
					s.escapeError([]rune{c}, "invalid escape sequence")
				}
				s.save(s.readDecimalEscape())
			}
		default:
			s.saveAndAdvance()
		}
	}
	s.saveAndAdvance()
	str := s.buffer.String()
	s.buffer.Reset()
	return token{t: tkString, s: str[1 : len(str)-1], raw: str}
}

func isReserved(s string) bool {
	for _, reserved := range tokens[:reservedCount] {
		if s == reserved {
			return true
		}
	}
	return false
}

func (s *scanner) reservedOrName() token {
	str := s.buffer.String()
	s.buffer.Reset()
	for i, reserved := range tokens[:reservedCount] {
		if str == reserved {
			return token{t: rune(i + firstReserved), s: reserved, raw: str}
		}
	}
	return token{t: tkName, s: str, raw: str}
}

func (s *scanner) scan() token {
	const comment, str = true, false
	for {
		switch c := s.current; c {
		case '\n', '\r':
			s.incrementLineNumber()
		case ' ', '\f', '\t', '\v':
			s.advance()
		case '/': // Lua 5.3: // for integer division
			if s.advance(); s.current == '/' {
				s.advance()
				return token{t: tkIDiv}
			}
			return token{t: '/'}
		case '-':
			if s.advance(); s.current != '-' {
				return token{t: '-'}
			}
			if s.advance(); s.current == '[' {
				if sep := s.skipSeparator(); sep >= 0 {
					_, _ = s.readMultiLine(comment, sep)
					break
				}
				s.buffer.Reset()
			}
			for !isNewLine(s.current) && s.current != endOfStream {
				s.advance()
			}
		case '[':
			if sep := s.skipSeparator(); sep >= 0 {
				content, rawStr := s.readMultiLine(str, sep)
				return token{t: tkString, s: content, raw: rawStr}
			} else if s.buffer.Reset(); sep == -1 {
				return token{t: '['}
			}
			s.scanError("invalid long string delimiter", tkString)
		case '=':
			if s.advance(); s.current != '=' {
				return token{t: '='}
			}
			s.advance()
			return token{t: tkEq}
		case '<':
			s.advance()
			if s.current == '=' {
				s.advance()
				return token{t: tkLE}
			} else if s.current == '<' { // Lua 5.3: <<
				s.advance()
				return token{t: tkShl}
			}
			return token{t: '<'}
		case '>':
			s.advance()
			if s.current == '=' {
				s.advance()
				return token{t: tkGE}
			} else if s.current == '>' { // Lua 5.3: >>
				s.advance()
				return token{t: tkShr}
			}
			return token{t: '>'}
		case '~':
			if s.advance(); s.current != '=' {
				return token{t: '~'}
			}
			s.advance()
			return token{t: tkNE}
		case ':':
			if s.advance(); s.current != ':' {
				return token{t: ':'}
			}
			s.advance()
			return token{t: tkDoubleColon}
		case '"', '\'':
			return s.readString()
		case endOfStream:
			return token{t: tkEOS}
		case '.':
			if s.saveAndAdvance(); s.checkNext(".") {
				if s.checkNext(".") {
					s.buffer.Reset()
					return token{t: tkDots}
				}
				s.buffer.Reset()
				return token{t: tkConcat}
			} else if !isDecimal(s.current) {
				s.buffer.Reset()
				return token{t: '.'}
			} else {
				return s.readNumber()
			}
		case 0:
			s.advance()
		default:
			if isDecimal(c) {
				return s.readNumber()
			} else if c == '_' || isAlpha(c) {
				for ; c == '_' || isAlpha(c) || isDecimal(c); c = s.current {
					s.saveAndAdvance()
				}
				return s.reservedOrName()
			}
			s.advance()
			return token{t: c}
		}
	}
}

func (s *scanner) next() {
	s.lastLine = s.lineNumber
	if s.lookAheadToken.t != tkEOS {
		s.token = s.lookAheadToken
		s.lookAheadToken.t = tkEOS
	} else {
		s.token = s.scan()
	}
	s.tokenBuf = s.token.raw
}

func (s *scanner) lookAhead() rune {
	s.l.assert(s.lookAheadToken.t == tkEOS)
	s.lookAheadToken = s.scan()
	return s.lookAheadToken.t
}

func (s *scanner) testNext(t rune) (r bool) {
	if r = s.t == t; r {
		s.next()
	}
	return
}

func (s *scanner) check(t rune) {
	if s.t != t {
		s.errorExpected(t)
	}
}

func (s *scanner) checkMatch(what, who rune, where int) {
	if !s.testNext(what) {
		if where == s.lineNumber {
			s.errorExpected(what)
		} else {
			s.syntaxError(fmt.Sprintf("%s expected (to close %s at line %d)", s.tokenToString(what), s.tokenToString(who), where))
		}
	}
}
