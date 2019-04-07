// Copyright 2017 The go-interpreter Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exec

import (
	"fmt"
	"math"
	"reflect"

	"github.com/go-interpreter/wagon/exec/internal/compile"
)

type function interface {
	call(vm *VM, index int64)
}

type named interface {
	Name() string
}

type compiledFunction struct {
	code           []byte
	codeMeta       *compile.BytecodeMetadata
	branchTables   []*compile.BranchTable
	maxDepth       int  // maximum stack depth reached while executing the function body
	totalLocalVars int  // number of local variables used by the function
	args           int  // number of arguments the function accepts
	returns        bool // whether the function returns a value
	name           string

	asm []asmBlock
}

type asmBlock struct {
	// Compiled unit in native machine code.
	nativeUnit compile.NativeCodeUnit
	// where in the instruction stream to resume after native execution.
	resumePC uint
}

type goFunction struct {
	val  reflect.Value
	typ  reflect.Type
	name string
}

func (fn goFunction) Name() string {
	return fn.name
}

func wasmToReflect(val *reflect.Value, i int, kind reflect.Kind, raw uint64) {
	switch kind {
	case reflect.Float64, reflect.Float32:
		val.SetFloat(math.Float64frombits(raw))
	case reflect.Uint32, reflect.Uint64:
		val.SetUint(raw)
	case reflect.Int32, reflect.Int64:
		val.SetInt(int64(raw))
	default:
		panic(fmt.Sprintf("exec: args %d invalid kind=%v", i, kind))
	}
}

func (fn goFunction) call(vm *VM, index int64) {
	fsig := vm.module.FunctionIndexSpace[index].Sig
	nparams := len(fsig.ParamTypes)

	proc := NewProcess(vm, index)
	// Adjust in case first argument is proc.
	// This allows to handle functions both with and without proc.
	startIndexForParams := 0
	if fn.typ.NumIn() > 0 && reflect.TypeOf(proc).ConvertibleTo(fn.typ.In(0)) {
		startIndexForParams = 1
		nparams++
	}
	args := make([]reflect.Value, nparams)

	// If the function is variadic, let's distinguish between the variadic
	// and non-variadic parts of params.
	endIndexForNonVariadicParams := nparams - 1
	isVariadic := fn.typ.IsVariadic()
	if isVariadic {
		endIndexForNonVariadicParams = fn.typ.NumIn() - 2
		// Since the last parameters are being popped first
		// let's handle the variadic ones first if any.
		typ := fn.typ.In(fn.typ.NumIn() - 1).Elem()
		kind := typ.Kind()
		// We populate until (and including) the index of the variadic slice parameter of the Go function.
		for i := nparams - 1; i >= endIndexForNonVariadicParams+1; i-- {
			val := reflect.New(typ).Elem()
			raw := vm.popUint64()
			wasmToReflect(&val, i, kind, raw)
			args[i] = val
		}
	}

	for i := endIndexForNonVariadicParams; i >= startIndexForParams; i-- {
		val := reflect.New(fn.typ.In(i)).Elem()
		raw := vm.popUint64()
		kind := fn.typ.In(i).Kind()
		wasmToReflect(&val, i, kind, raw)
		args[i] = val
	}

	// Add proc as first argument if function needs it.
	if startIndexForParams > 0 {
		args[0] = reflect.ValueOf(proc)
	}

	rtrns := fn.val.Call(args)
	for i, out := range rtrns {
		kind := out.Kind()
		switch kind {
		case reflect.Float64, reflect.Float32:
			vm.pushFloat64(out.Float())
		case reflect.Uint32, reflect.Uint64:
			vm.pushUint64(out.Uint())
		case reflect.Int32, reflect.Int64:
			vm.pushInt64(out.Int())
		default:
			panic(fmt.Sprintf("exec: return value %d invalid kind=%v", i, kind))
		}
	}
}

func (compiled compiledFunction) call(vm *VM, index int64) {
	// Make space on the stack for all intermediate values and
	// a possible return value.
	newStack := make([]uint64, 0, compiled.maxDepth+1)
	locals := make([]uint64, compiled.totalLocalVars)

	for i := compiled.args - 1; i >= 0; i-- {
		locals[i] = vm.popUint64()
	}

	//save execution context
	prevCtxt := vm.ctx

	vm.ctx = context{
		stack:   newStack,
		locals:  locals,
		code:    compiled.code,
		asm:     compiled.asm,
		pc:      0,
		curFunc: index,
	}

	rtrn := vm.execCode(compiled)

	//restore execution context
	vm.ctx = prevCtxt

	if compiled.returns {
		vm.pushUint64(rtrn)
	}
}

func (compiled compiledFunction) Name() string {
	return compiled.name
}
