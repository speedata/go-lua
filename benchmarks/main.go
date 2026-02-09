// Benchmark runner for go-lua.
// Runs all .lua files in the benchmarks directory and reports wall-clock times.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	lua "github.com/speedata/go-lua"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.lua"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(files)

	for _, path := range files {
		fmt.Printf("--- %s ---\n", filepath.Base(path))
		l := lua.NewState()
		lua.OpenLibraries(l)

		start := time.Now()
		if err := lua.DoFile(l, path); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			continue
		}
		fmt.Printf("  wall time: %.3f s\n\n", time.Since(start).Seconds())
	}
}
