package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tetratelabs/wazero"
)

func main() {
	path := os.Args[1]
	wasm, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		panic(err)
	}
	fmt.Printf("imports=%d\n", len(mod.ImportedFunctions()))
	for _, fn := range mod.ImportedFunctions() {
		modName, name, _ := fn.Import()
		fmt.Printf("%s.%s params=%v results=%v\n", modName, name, fn.ParamTypes(), fn.ResultTypes())
	}
	fmt.Printf("exports=%d\n", len(mod.ExportedFunctions()))
	for _, fn := range mod.ExportedFunctions() {
		for _, name := range fn.ExportNames() {
			fmt.Printf("%s params=%v results=%v\n", name, fn.ParamTypes(), fn.ResultTypes())
		}
	}
}
