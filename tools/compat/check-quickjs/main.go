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
		fmt.Printf("%s.%s\n", modName, name)
	}
	fmt.Printf("exports=%d\n", len(mod.ExportedFunctions()))
	for _, fn := range mod.ExportedFunctions() {
		fmt.Printf("%s\n", fn.Name())
	}
}
