-- Tight loop with arithmetic
local start = os.clock()
local sum = 0
for i = 1, 10000000 do
  sum = sum + i * 2 - 1
end
local elapsed = os.clock() - start
print(string.format("loop sum = %d, time: %.3f s", sum, elapsed))
