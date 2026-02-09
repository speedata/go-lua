# Benchmarks: go-lua vs. C-Lua 5.3

Simple benchmarks to compare go-lua performance against the reference C implementation of Lua 5.3.

## Lua scripts

| Script       | What it measures                                      |
|--------------|-------------------------------------------------------|
| `fib.lua`    | Recursive Fibonacci — function call overhead          |
| `loop.lua`   | Tight arithmetic loop (10M iterations) — VM dispatch  |
| `table.lua`  | Array insert/read (1M), hash insert/iterate (500k)    |
| `string.lua` | table.concat (100k), string.gmatch pattern matching   |
| `sort.lua`   | table.sort on 500k random integers                    |

## Running the benchmarks

### go-lua

```bash
cd benchmarks
go run . .
```

Or build and run:

```bash
cd benchmarks
go build -o run-benchmarks .
./run-benchmarks .
```

### C-Lua 5.3 (for comparison)

```bash
for f in benchmarks/*.lua; do
  echo "--- $(basename $f) ---"
  lua5.3 "$f"
  echo
done
```

## Results

Measured on Apple M4, macOS, Go 1.24, Lua 5.3.6.

| Benchmark   | C-Lua 5.3 | go-lua  | Factor |
|-------------|-----------|---------|--------|
| fib(35)     | 0.42 s    | 1.02 s  | ~2.4x  |
| loop 10M    | 0.05 s    | 0.39 s  | ~8x    |
| table       | 0.22 s    | 0.63 s  | ~3x    |
| string      | 0.02 s    | 0.05 s  | ~2.5x  |
| sort 500k   | 0.11 s    | 0.57 s  | ~5x    |

go-lua is roughly **2-8x** slower than C-Lua 5.3, which is expected for a pure Go implementation. The overhead comes mainly from Go interface dispatch, bounds checking, and garbage collection differences.
