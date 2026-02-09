-- Recursive Fibonacci (CPU-intensive, function calls)
local function fib(n)
  if n < 2 then return n end
  return fib(n-1) + fib(n-2)
end

local start = os.clock()
local result = fib(35)
local elapsed = os.clock() - start
print(string.format("fib(35) = %d, time: %.3f s", result, elapsed))
