package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/go-interpreter/wagon/wasm"
)

func main() {
	flag.Parse()
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := decode(f); err != nil {
		panic(err)
	}
}

func decode(f *os.File) error {
	m, err := wasm.DecodeModule(f)
	if err != nil {
		return err
	}
	fmt.Println(m)
	return nil
}
