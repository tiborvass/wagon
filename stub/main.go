// Copyright 2017 The go-interpreter Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"syscall"
	"unsafe"

	"github.com/go-interpreter/wagon/exec"
	"github.com/go-interpreter/wagon/validate"
	"github.com/go-interpreter/wagon/wasm"
)

type stub struct{}

func (stub) __syscall_ioctl(fd int32, request uint32, value int32, _, _, _ uint32) (r int32) {
	fmt.Println("+syscall_ioctl:", fd, request, value)
	var err error
	defer func() {
		if err != nil {
			r = -1
		}
		fmt.Println("-syscall_ioctl:", err, r)
	}()
	return -1
	/*
		switch int(fd) {
		case syscall.Stdin, syscall.Stdout, syscall.Stderr:
			fmt.Println("value", value)
			if value != 0 {
				err = unix.IoctlSetInt(int(fd), uint(request), int(value))
				if err != nil {
					return -1
				}
			} else {
				var v int
				v, err = unix.IoctlGetInt(int(fd), uint(request))
				if err != nil {
					return -1
				}
				return int32(v)
			}
			return 0
		}
		panic(fmt.Errorf("fd %d not handled", fd))
	*/
}

func (stub) __syscall_writev(proc *exec.Process, fd int32, iov uint32, iovcnt int32, _, _, _ uint32) (r int32) {
	fmt.Println("+syscall_writev:", fd, iov, iovcnt)
	var err error
	defer func() {
		if err != nil {
			r = -1
		}
		fmt.Println("-syscall_writev:", err, r)
	}()
	switch int(fd) {
	case syscall.Stdin, syscall.Stdout, syscall.Stderr:
		mem := proc.VM().Memory()
		buf := make([]syscall.Iovec, iovcnt)
		for i := 0; i < int(iovcnt); i++ {
			iov_base := binary.LittleEndian.Uint32(mem[iov : iov+4])
			iov += 4
			iov_len := binary.LittleEndian.Uint32(mem[iov : iov+4])
			iov += 4
			buf[i] = syscall.Iovec{&mem[iov_base], uint64(iov_len)}
		}
		var p unsafe.Pointer = unsafe.Pointer(&buf[0])
		n, _, errno := syscall.Syscall6(syscall.SYS_WRITEV, uintptr(fd), uintptr(p), uintptr(iovcnt), 0, 0, 0)
		if errno != 0 {
			err = syscall.Errno(errno)
		}
		return int32(n)
	}
	panic(fmt.Errorf("fd %d not handled", fd))
}

func main() {
	log.SetPrefix("wasm-run: ")
	log.SetFlags(0)

	verbose := flag.Bool("v", false, "enable/disable verbose mode")
	verify := flag.Bool("verify-module", false, "run module verification")

	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	wasm.SetDebugMode(*verbose)

	run(os.Stdout, flag.Arg(0), *verify)
}

func run(w io.Writer, fname string, verify bool) {
	f, err := os.Open(fname)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	em := newEmbedder()

	var s stub

	em.AddModule("env", map[string]interface{}{
		// undefined function handler
		"": func(proc *exec.Process, v ...uint64) uint64 {
			d := proc.Debug()
			fmt.Printf("undefined handler: %v %v\n", d, v)
			x := 1
			return uint64(x)
		},
		"__syscall_ioctl":  s.__syscall_ioctl,
		"__syscall_writev": s.__syscall_writev,
	})

	m, err := wasm.ReadModule(f, em.ResolveImport)
	if err != nil {
		log.Fatalf("could not read module: %v", err)
	}

	if verify {
		err = validate.VerifyModule(m)
		if err != nil {
			log.Fatalf("could not verify module: %v", err)
		}
	}

	if m.Export == nil {
		log.Fatalf("module has no export section")
	}

	vm, err := exec.NewVM(m)
	if err != nil {
		log.Fatalf("could not create VM: %v", err)
	}

	name := "add"
	e, ok := m.Export.Entries[name]
	if !ok {
		log.Fatal("main function not found")
	}
	i := int64(e.Index)
	fidx := m.Function.Types[int(i)]
	ftype := m.Types.Entries[int(fidx)]
	if len(ftype.ReturnTypes) > 1 {
		log.Fatalf("running exported functions with more than one return value is not supported")
	}
	o, err := vm.ExecCode(i, 8, 7)
	if err != nil {
		fmt.Fprintf(w, "\n")
		log.Fatalf("err=%v", err)
	}
	if len(ftype.ReturnTypes) == 0 {
		fmt.Fprintf(w, "\n")
	}
	fmt.Fprintf(w, "%[1]v (%[1]T)\n", o)
}

type embedder struct {
	modules map[string]*wasm.Module
}

func newEmbedder() *embedder {
	return &embedder{modules: make(map[string]*wasm.Module)}
}

func (e *embedder) AddModule(module string, funcs map[string]interface{}) {
	m := wasm.NewModule()
	for name, fn := range funcs {
		m.AddHostFunc(name, fn)
	}
	e.modules[module] = m
}

func (e *embedder) ResolveImport(name string) (*wasm.Module, error) {
	m, ok := e.modules[name]
	if !ok {
		return nil, fmt.Errorf("could not import %s", name)
	}
	return m, nil
}
