# go-lua

A Lua 5.3 VM in pure Go

> **Note:** This is a fork of [Shopify/go-lua](https://github.com/Shopify/go-lua) with Lua 5.3 support.

## Overview

go-lua is a port of the Lua VM to pure Go. It is compatible with binary files dumped by `luac` from the [Lua reference implementation](http://www.lua.org/).

This fork upgrades the original Lua 5.2 implementation to **Lua 5.3**, adding:

- Native 64-bit integers (`int64`) separate from floats (`float64`)
- Bitwise operators: `&`, `|`, `~`, `<<`, `>>` and unary `~`
- Integer division operator: `//`
- UTF-8 library: `utf8.char`, `utf8.codes`, `utf8.codepoint`, `utf8.len`, `utf8.offset`
- String packing: `string.pack`, `string.unpack`, `string.packsize`
- Math extensions: `math.tointeger`, `math.type`, `math.ult`, `math.maxinteger`, `math.mininteger`
- Table move: `table.move(a1, f, e, t [,a2])`
- Hex float format: `string.format` supports `%a`/`%A`

## Installation

```sh
go get github.com/speedata/go-lua
```

## Usage

go-lua is intended to be used as a Go package. A simple example:

```go
package main

import "github.com/speedata/go-lua"

func main() {
    l := lua.NewState()
    lua.OpenLibraries(l)
    if err := lua.DoFile(l, "hello.lua"); err != nil {
        panic(err)
    }
}
```

### Calling Lua from Go

```go
l := lua.NewState()
lua.OpenLibraries(l)

// Load and execute a Lua script
lua.DoString(l, `
    function greet(name)
        return "Hello, " .. name .. "!"
    end
`)

// Call the Lua function
l.Global("greet")
l.PushString("World")
l.Call(1, 1)
result, _ := l.ToString(-1)
fmt.Println(result) // Output: Hello, World!
```

### Registering Go functions in Lua

```go
l := lua.NewState()
lua.OpenLibraries(l)

// Register a Go function
l.Register("add", func(l *lua.State) int {
    a := lua.CheckNumber(l, 1)
    b := lua.CheckNumber(l, 2)
    l.PushNumber(a + b)
    return 1
})

lua.DoString(l, `print(add(2, 3))`) // Output: 5
```

## Status

### Lua 5.3 Compatibility

This implementation passes **12 of 13 core Lua 5.3 test suites**:

| Test | Status |
|------|--------|
| bitwise | ✅ Pass |
| code | ✅ Pass |
| constructs | ✅ Pass |
| events | ✅ Pass |
| goto | ✅ Pass |
| locals | ✅ Pass |
| math | ✅ Pass |
| pm (pattern matching) | ✅ Pass |
| sort | ✅ Pass |
| tpack (string.pack) | ✅ Pass |
| utf8 | ✅ Pass |
| vararg | ✅ Pass |
| strings | ⚠️ Requires coroutines |

### Known Limitations

- **No coroutines**: `coroutine.*` functions are not implemented.
- **No weak references**: Lua's weak tables (`__mode`) are not implemented.
- **No `string.dump`**: Serializing functions to bytecode is not supported.
- **No C libraries**: C Lua libraries are incompatible with this pure Go implementation.

### What Works Well

- All arithmetic and bitwise operations with proper integer/float semantics
- Tables, metatables, and metamethods
- Closures and upvalues
- Pattern matching (`string.find`, `string.match`, `string.gmatch`, `string.gsub`)
- All standard libraries except coroutines
- Loading precompiled bytecode (`.luac` files)
- Debug hooks (with slight performance cost)

## Development

```sh
# Clone with test submodule
git clone --recursive https://github.com/speedata/go-lua.git

# Or initialize submodule after cloning
git submodule update --init

# Build
go build

# Run tests (requires luac 5.3 in PATH)
PATH="$PWD/lua-5.3.6/src:$PATH" go test -v ./...

# Build luac 5.3 if needed
cd lua-5.3.6 && make macosx  # or: make linux
```

## Performance

go-lua prioritizes correctness and compatibility over raw performance. It includes debug hooks which add overhead but enable powerful debugging capabilities.

Compared to C Lua 5.3:
- Recursive function calls: ~6x slower
- Tail calls: ~6x slower
- Tight loops: ~10x slower

This is typical for pure Go Lua implementations and sufficient for configuration, scripting, and workflow automation use cases.

## License

go-lua is licensed under the [MIT License](LICENSE.md).

This is a fork of [Shopify/go-lua](https://github.com/Shopify/go-lua). Original work Copyright (c) Shopify Inc.
