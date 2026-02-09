-- Table operations (insert, read, hash, iteration)
local start = os.clock()
local t = {}
for i = 1, 1000000 do
  t[i] = i * 3
end
local sum = 0
for i = 1, #t do
  sum = sum + t[i]
end
-- Hash part
local h = {}
for i = 1, 500000 do
  h["key" .. i] = i
end
local hsum = 0
for k, v in pairs(h) do
  hsum = hsum + v
end
local elapsed = os.clock() - start
print(string.format("table sum=%d hsum=%d, time: %.3f s", sum, hsum, elapsed))
