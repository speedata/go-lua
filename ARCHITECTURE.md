# go-lua Architecture

A guided tour of the Lua VM internals for Go developers.

## The Big Picture

go-lua is a from-scratch implementation of the Lua 5.3 virtual machine in pure Go. No CGo, no bindings - just Go code interpreting Lua bytecode.

```
┌─────────────────────────────────────────────────────────────────┐
│                         Your Go App                             │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                        lua.State                                │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────────────────┐ │
│  │  Stack  │  │ Globals │  │ Registry│  │ Standard Libraries  │ │
│  └─────────┘  └─────────┘  └─────────┘  └─────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                      VM Execution Loop                          │
│            (fetch instruction → decode → execute)               │
└─────────────────────────────────────────────────────────────────┘
```

## Compilation Pipeline

When you call `lua.LoadString()` or `lua.LoadFile()`, here's what happens:

```
   Lua Source Code
         │
         ▼
   ┌───────────┐
   │  Scanner  │  scanner.go - tokenizes source into tokens
   └───────────┘
         │
         ▼
   ┌───────────┐
   │  Parser   │  parser.go - builds AST, validates syntax
   └───────────┘
         │
         ▼
   ┌───────────┐
   │   Code    │  code.go - generates bytecode instructions
   │ Generator │
   └───────────┘
         │
         ▼
   ┌───────────┐
   │ Prototype │  The compiled function (bytecode + metadata)
   └───────────┘
```

**Important**: Lua compilation is single-pass. The parser and code generator work together - bytecode is emitted as the source is parsed. There's no separate AST data structure.

## The Stack

Everything in Lua revolves around the stack. If you understand the stack, you understand 80% of how Lua works.

```go
// In lua.go
type State struct {
    stack     []value    // The value stack
    top       int        // First free slot
    callInfo  *callInfo  // Current call frame
    // ... more fields
}
```

The stack holds all temporary values, function arguments, and return values:

```
Stack indices:

  Positive (from bottom)     Negative (from top)

       ┌─────────┐
    5  │  arg2   │  -1  (top of stack)
       ├─────────┤
    4  │  arg1   │  -2
       ├─────────┤
    3  │  func   │  -3
       ├─────────┤
    2  │  local2 │  -4
       ├─────────┤
    1  │  local1 │  -5
       └─────────┘
```

When you call `l.PushString("hello")`, it goes onto the stack. When you call `l.ToString(-1)`, you're reading the top element.

### Stack Operations

```go
// Push values onto the stack
l.PushNil()
l.PushBoolean(true)
l.PushInteger(42)
l.PushNumber(3.14)
l.PushString("hello")

// Read values from the stack
s, _ := l.ToString(-1)   // Read top as string
n, _ := l.ToNumber(-2)   // Read second from top as number
l.ToBoolean(1)           // Read first element as boolean

// Stack manipulation
l.Pop(2)                 // Remove top 2 elements
l.PushValue(-1)          // Duplicate top element
l.Remove(3)              // Remove element at index 3
```

## Values and Types

Lua is dynamically typed. The `value` type in Go is just `interface{}`:

```go
// In types.go
type value interface{}
```

Here's how Lua types map to Go:

| Lua Type       | Go Representation             |
| -------------- | ----------------------------- |
| nil            | `nil`                         |
| boolean        | `bool`                        |
| integer        | `int64`                       |
| number (float) | `float64`                     |
| string         | `string`                      |
| table          | `*table`                      |
| function       | `*luaClosure` or `*goClosure` |
| userdata       | `*userData`                   |

### The Integer/Float Distinction (Lua 5.3)

Lua 5.3 introduced proper integers. The VM tracks whether a number is `int64` or `float64`:

```go
// In types.go
func toInteger(v value) (int64, bool) {
    switch n := v.(type) {
    case int64:
        return n, true
    case float64:
        // Only convert if it's a whole number in range
        if i := int64(n); float64(i) == n {
            return i, true
        }
    }
    return 0, false
}
```

This matters for bitwise operations (integers only) and the `//` operator (integer division).

## Tables

Tables are Lua's only data structure - they're used for arrays, dictionaries, objects, modules, and namespaces.

```go
// In table.go
type table struct {
    array     []value           // Integer keys 1..n
    hash      map[value]value   // Everything else
    metaTable *table            // For operator overloading
}
```

The implementation uses a hybrid approach:
- **Array part**: For consecutive integer keys starting at 1
- **Hash part**: For everything else (strings, non-consecutive numbers, etc.)

```lua
-- In Lua:
t = {10, 20, 30, name = "test"}

-- Internal representation:
-- array: [10, 20, 30]
-- hash:  {"name" -> "test"}
```

### Table Access from Go

```go
l.NewTable()              // Push empty table
l.SetField(-1, "key")     // t.key = (top of stack)
l.Field(-1, "key")        // Push t.key onto stack
l.RawSetInt(-1, 1)        // t[1] = (top of stack), no metamethods
```

## Closures and Upvalues

This is where it gets interesting. A closure is a function plus its captured variables (upvalues).

```lua
function counter()
    local count = 0           -- This is captured
    return function()
        count = count + 1     -- Accessing upvalue
        return count
    end
end

local c = counter()
print(c())  -- 1
print(c())  -- 2
```

In Go:

```go
// In stack.go
type luaClosure struct {
    prototype *prototype    // The bytecode
    upValues  []*upValue    // Captured variables
}

type upValue struct {
    home interface{}  // Either stackLocation or the value itself
}

type stackLocation struct {
    state *State
    index int
}
```

**The clever bit**: While the outer function is still running, the upvalue points to a stack slot (`stackLocation`). When the outer function returns, the upvalue is "closed" - the value is copied into the upValue struct itself.

```
Before outer function returns:        After outer function returns:

upValue.home ──► stackLocation        upValue.home ──► value (42)
                    │
                    ▼                 (stack slot is gone)
              stack[index] = 42
```

## The VM Execution Loop

The heart of the interpreter is in `vm.go`. It's a big switch statement over opcodes:

```go
// Simplified from vm.go
func (l *State) execute() {
    ci := l.callInfo
    frame := ci.frame  // Current stack frame

    for {
        i := ci.step()  // Fetch next instruction

        switch i.opCode() {
        case opMove:
            frame[i.a()] = frame[i.b()]

        case opLoadConstant:
            frame[i.a()] = constants[i.bx()]

        case opAdd:
            // Get operands, possibly from constants
            b := frame[i.b()] or constants[i.b()]
            c := frame[i.c()] or constants[i.c()]
            frame[i.a()] = add(b, c)

        case opCall:
            // Set up new call frame, recurse or call Go function

        case opReturn:
            // Pop call frame, copy results
            return

        // ... 40+ more opcodes
        }
    }
}
```

### Instructions

Each instruction is a 32-bit integer packed with opcode and operands:

```
┌────────┬────────┬────────┬────────┐
│ opcode │   A    │   B    │   C    │  (ABC format)
│ 6 bits │ 8 bits │ 9 bits │ 9 bits │
└────────┴────────┴────────┴────────┘

┌────────┬────────┬─────────────────┐
│ opcode │   A    │       Bx        │  (ABx format)
│ 6 bits │ 8 bits │     18 bits     │
└────────┴────────┴─────────────────┘
```

See `instructions.go` for the encoding/decoding.

## Call Frames

Each function call gets a `callInfo` struct:

```go
// In stack.go
type callInfo struct {
    function    int         // Stack index of the function
    top         int         // Top of this frame's stack
    resultCount int         // Expected number of results
    previous    *callInfo   // Linked list of frames
    next        *callInfo
    *luaCallInfo            // For Lua functions
    *goCallInfo             // For Go functions
}

type luaCallInfo struct {
    frame   []value       // Slice into the main stack
    savedPC pc            // Current instruction pointer
    code    []instruction // Bytecode
}
```

When you call a function:
1. Arguments are already on the stack
2. A new `callInfo` is created
3. `frame` is set to a slice of the stack for this call
4. The VM executes until `opReturn`
5. Results are copied to where the caller expects them
6. `callInfo` is popped

## Go ↔ Lua Interop

### Calling Go from Lua

Register a Go function:

```go
l.Register("greet", func(l *lua.State) int {
    name := lua.CheckString(l, 1)  // Get first argument
    l.PushString("Hello, " + name + "!")
    return 1  // Number of return values
})
```

Go functions receive arguments on the stack and push return values. The return value of the Go function tells Lua how many values to pop as results.

### Calling Lua from Go

```go
l.Global("myfunction")    // Push the function
l.PushInteger(42)         // Push argument
l.Call(1, 1)              // 1 arg, 1 result
result, _ := l.ToInteger(-1)
l.Pop(1)
```

## Metatables and Metamethods

Metatables enable operator overloading and custom behavior. When the VM encounters an operation, it checks for metamethods:

```go
// Simplified from vm.go
func (l *State) add(a, b value) value {
    // Try normal addition first
    if na, nb, ok := pairAsNumbers(a, b); ok {
        return na + nb
    }
    // Fall back to metamethod
    if tm := l.tagMethodByObject(a, tmAdd); tm != nil {
        return l.callMetamethod(tm, a, b)
    }
    if tm := l.tagMethodByObject(b, tmAdd); tm != nil {
        return l.callMetamethod(tm, a, b)
    }
    l.typeError(a, "perform arithmetic on")
}
```

Common metamethods:
- `__add`, `__sub`, `__mul`, `__div` - arithmetic
- `__index` - table access (reading)
- `__newindex` - table access (writing)
- `__call` - calling as a function
- `__tostring` - string conversion

## Memory Management

Here's the easy part: **Go's garbage collector handles everything**.

Unlike C Lua, which has its own GC, go-lua just allocates Go objects. When they're no longer referenced, Go cleans them up. This is why weak tables aren't supported - Go doesn't expose weak references.

## File Guide

| File                                    | What's in it                            |
| --------------------------------------- | --------------------------------------- |
| `lua.go`                                | `State` type, public API                |
| `vm.go`                                 | Bytecode interpreter                    |
| `stack.go`                              | Stack operations, closures, call frames |
| `parser.go`                             | Recursive descent parser                |
| `scanner.go`                            | Lexer/tokenizer                         |
| `code.go`                               | Bytecode generator                      |
| `types.go`                              | Type conversions, prototypes            |
| `table.go`                              | Table implementation                    |
| `instructions.go`                       | Bytecode instruction encoding           |
| `dump.go` / `undump.go`                 | Bytecode serialization                  |
| `base.go`, `string.go`, `math.go`, etc. | Standard libraries                      |

## Performance Notes

go-lua is roughly 6-10x slower than C Lua. This is typical for pure Go interpreters:

- **No JIT**: Everything is interpreted
- **Interface dispatch**: Using `value interface{}` means type switches everywhere
- **Go's switch**: Not as optimized as computed gotos in C
- **Debug hooks**: Always enabled, even when not used

For configuration files and light scripting, this is perfectly fine. For heavy computation, consider calling optimized Go code from Lua.

## Further Reading

- [Lua 5.3 Reference Manual](https://www.lua.org/manual/5.3/)
- [The Implementation of Lua 5.0](https://www.lua.org/doc/jucs05.pdf) - The classic paper
- [A No-Frills Introduction to Lua 5.1 VM Instructions](http://luaforge.net/docman/83/98/ANoFrillsIntroToLua51VMInstructions.pdf)
