# go-lua

A Lua 5.4 VM in pure Go — no CGo, no dependencies.

This is a fork of [Shopify/go-lua](https://github.com/Shopify/go-lua), upgraded from Lua 5.3 to **Lua 5.4**.

## What's new compared to Shopify/go-lua?

- Native 64-bit integers (`int64`) alongside floats (`float64`)
- Bitwise operators: `&`, `|`, `~`, `<<`, `>>` and unary `~`
- Integer division: `//`
- Coroutines: `coroutine.create`, `resume`, `yield`, `wrap`, `status`, `running`, `close`, `isyieldable`
- UTF-8 library: `utf8.char`, `utf8.codes`, `utf8.codepoint`, `utf8.len`, `utf8.offset`
- String packing: `string.pack`, `string.unpack`, `string.packsize`
- String dump: `string.dump` (with strip option)
- Math extensions: `math.tointeger`, `math.type`, `math.ult`, `math.maxinteger`, `math.mininteger`
- Table move: `table.move(a1, f, e, t [,a2])`
- Table metamethods: `table.insert`, `table.remove`, `table.sort` respect `__index`/`__newindex`
- Hex float format: `string.format` supports `%a`/`%A`
- To-be-closed variables: `<close>` attribute and `__close` metamethod
- Const variables: `<const>` attribute
- Generalized `for` with to-be-closed control variable
- `warn()` function
- Debug library: `debug.getlocal`, `debug.setlocal`, `debug.getinfo`, `debug.sethook` (including coroutine hooks)

## Getting started

```sh
go get github.com/speedata/go-lua
```

A minimal example:

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

lua.DoString(l, `
    function greet(name)
        return "Hello, " .. name .. "!"
    end
`)

l.Global("greet")
l.PushString("World")
l.Call(1, 1)
result, _ := l.ToString(-1)
fmt.Println(result) // Hello, World!
```

### Registering Go functions in Lua

```go
l := lua.NewState()
lua.OpenLibraries(l)

l.Register("add", func(l *lua.State) int {
    a := lua.CheckNumber(l, 1)
    b := lua.CheckNumber(l, 2)
    l.PushNumber(a + b)
    return 1
})

lua.DoString(l, `print(add(2, 3))`) // 5
```

## Test suite status

We run the official Lua 5.4 test suites. Currently **21 out of 25** pass:

| Test | Status | Notes |
|------|--------|-------|
| bitwise | Pass | |
| calls | Pass | |
| closure | Pass | |
| code | Pass | |
| constructs | Pass | |
| coroutine | Pass | |
| db (debug) | Pass | |
| errors | Pass | |
| events | Pass | |
| files | Pass | |
| goto | Pass | |
| literals | Pass | |
| locals | Pass | |
| math | Pass | |
| nextvar | Pass | |
| pm (pattern matching) | Pass | |
| sort | Pass | |
| strings | Pass | |
| tpack (string.pack) | Pass | |
| utf8 | Pass | |
| vararg | Pass | |
| attrib | — | Needs weak references |
| gc | — | Go's GC, not controllable like Lua's |
| big | — | Tables with >2^18 elements |
| main | — | Requires standalone Lua binary |

## Known limitations

- **No weak references** — `__mode` on metatables is not supported (Go's GC doesn't offer that hook)
- **No C API** — pure Go, so C Lua libraries won't work (that's kind of the point though)

## Development

```sh
git clone https://github.com/speedata/go-lua.git
go build ./...
go test ./...
```

Some tests optionally use `luac` 5.4 for compiling Lua source to bytecode. If it's not in your PATH, those tests get skipped automatically.

## License

MIT — see [LICENSE.md](LICENSE.md).

Originally forked from [Shopify/go-lua](https://github.com/Shopify/go-lua).
