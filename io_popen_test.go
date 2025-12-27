package lua

import "testing"

func TestPopen(t *testing.T) {
	testString(t, `
		-- Test popen read mode
		local f = io.popen("echo hello")
		assert(f, "popen failed")
		local line = f:read("l")
		assert(line == "hello", "popen read failed: got '" .. tostring(line) .. "'")
		local ok = f:close()
		assert(ok == true, "popen close should return true on success")
		print("popen read: OK")

		-- Test popen with multiple lines
		f = io.popen("echo 'line1'; echo 'line2'")
		local l1 = f:read("l")
		local l2 = f:read("l")
		assert(l1 == "line1", "line1 failed: got '" .. tostring(l1) .. "'")
		assert(l2 == "line2", "line2 failed: got '" .. tostring(l2) .. "'")
		f:close()
		print("popen multi-line: OK")

		-- Test popen with exit code
		f = io.popen("exit 0")
		f:read("a")
		local ok = f:close()
		assert(ok == true, "exit 0 should succeed")
		print("popen exit 0: OK")

		-- Test popen with non-zero exit
		f = io.popen("exit 42")
		f:read("a")
		local ok, err, code = f:close()
		assert(ok == nil, "exit 42 should fail")
		assert(code == 42, "exit code should be 42, got " .. tostring(code))
		print("popen exit 42: OK")

		-- Test popen write mode
		local tmp = os.tmpname()
		f = io.popen("cat > " .. tmp, "w")
		f:write("test data\n")
		f:close()
		-- Verify the data was written
		local rf = io.open(tmp, "r")
		local content = rf:read("a")
		rf:close()
		os.remove(tmp)
		assert(content == "test data\n", "popen write failed: got '" .. tostring(content) .. "'")
		print("popen write: OK")

		print("\nAll popen tests passed!")
	`)
}
