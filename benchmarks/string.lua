-- String operations (concatenation, pattern matching)
local start = os.clock()
local parts = {}
for i = 1, 100000 do
  parts[i] = tostring(i)
end
local big = table.concat(parts, ",")

local count = 0
for w in big:gmatch("%d+") do
  count = count + 1
end
local elapsed = os.clock() - start
print(string.format("strings: len=%d count=%d, time: %.3f s", #big, count, elapsed))
