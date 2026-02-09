-- Table sort benchmark
math.randomseed(42)
local start = os.clock()
local t = {}
for i = 1, 500000 do
  t[i] = math.random(1, 1000000)
end
table.sort(t)
local elapsed = os.clock() - start
print(string.format("sort 500k elements, time: %.3f s", elapsed))
