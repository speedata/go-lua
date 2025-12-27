package lua

import "testing"

func TestIORead(t *testing.T) {
	testString(t, `
		-- Test file read functionality
		local tmp = os.tmpname()
		
		-- Write test data
		local f = io.open(tmp, "w")
		assert(f, "cannot open temp file for writing")
		f:write("hello\n")
		f:write("world\n")
		f:write("123\n")
		f:write("45.67\n")
		f:write("0xABC\n")
		f:close()
		
		-- Test read("l") - read line without EOL
		f = io.open(tmp, "r")
		local line = f:read("l")
		assert(line == "hello", "read('l') failed: got '" .. tostring(line) .. "'")
		print("read('l'):", line, "OK")
		
		-- Test read("*l") - Lua 5.2 format
		line = f:read("*l")
		assert(line == "world", "read('*l') failed: got '" .. tostring(line) .. "'")
		print("read('*l'):", line, "OK")
		
		-- Test read("n") - read number (integer)
		local num = f:read("n")
		assert(num == 123, "read('n') for int failed: got " .. tostring(num))
		print("read('n') int:", num, "OK")
		
		-- Test read("n") - read number (float)
		num = f:read("n")
		assert(num == 45.67, "read('n') for float failed: got " .. tostring(num))
		print("read('n') float:", num, "OK")
		
		-- Test read("n") - read hex number
		num = f:read("n")
		assert(num == 0xABC, "read('n') for hex failed: got " .. tostring(num))
		print("read('n') hex:", num, "OK")
		
		f:close()
		
		-- Test read("a") - read all
		f = io.open(tmp, "r")
		local all = f:read("a")
		assert(#all > 0, "read('a') failed")
		print("read('a'):", #all, "bytes OK")
		f:close()
		
		-- Test read("L") - read line with EOL
		f = io.open(tmp, "r")
		line = f:read("L")
		assert(line == "hello\n", "read('L') failed: got '" .. tostring(line) .. "'")
		print("read('L'):", "OK")
		f:close()
		
		-- Test read(n) - read n bytes
		f = io.open(tmp, "r")
		local bytes = f:read(5)
		assert(bytes == "hello", "read(5) failed: got '" .. tostring(bytes) .. "'")
		print("read(5):", bytes, "OK")
		f:close()
		
		-- Test read() - default is "l"
		f = io.open(tmp, "r")
		line = f:read()
		assert(line == "hello", "read() default failed: got '" .. tostring(line) .. "'")
		print("read():", line, "OK")
		f:close()
		
		-- Test read(0) - test for EOF
		f = io.open(tmp, "r")
		local test = f:read(0)
		assert(test == "", "read(0) at start should return ''")
		f:read("a") -- read all
		test = f:read(0)
		assert(test == nil, "read(0) at EOF should return nil")
		print("read(0): OK")
		f:close()
		
		-- Cleanup
		os.remove(tmp)
		print("\nAll read tests passed!")
	`)
}
