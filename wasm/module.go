// Copyright 2017 The go-interpreter Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/go-interpreter/wagon/wasm/internal/readpos"
)

var ErrInvalidMagic = errors.New("wasm: Invalid magic number")

const (
	Magic   uint32 = 0x6d736100
	Version uint32 = 0x1
)

var reflectToValueType = map[reflect.Kind]ValueType{
	reflect.Int32:   ValueTypeI32,
	reflect.Int64:   ValueTypeI64,
	reflect.Uint32:  ValueTypeI32,
	reflect.Uint64:  ValueTypeI64,
	reflect.Float32: ValueTypeF32,
	reflect.Float64: ValueTypeF64,
}

// Function represents an entry in the function index space of a module.
type Function struct {
	Sig  *FunctionSig
	Body *FunctionBody
	Host reflect.Value
	Name string
}

// IsHost indicates whether this function is a host function as defined in:
//  https://webassembly.github.io/spec/core/exec/modules.html#host-functions
func (fct *Function) IsHost() bool {
	return fct.Host != reflect.Value{}
}

// Module represents a parsed WebAssembly module:
// http://webassembly.org/docs/modules/
type Module struct {
	Version  uint32
	Sections []Section

	Types    *SectionTypes
	Import   *SectionImports
	Function *SectionFunctions
	Table    *SectionTables
	Memory   *SectionMemories
	Global   *SectionGlobals
	Export   *SectionExports
	Start    *SectionStartFunction
	Elements *SectionElements
	Code     *SectionCode
	Data     *SectionData
	Customs  []*SectionCustom

	// The function index space of the module
	FunctionIndexSpace []Function
	GlobalIndexSpace   []GlobalEntry

	// function indices into the global function space
	// the limit of each table is its capacity (cap)
	TableIndexSpace        [][]uint32
	LinearMemoryIndexSpace [][]byte

	imports struct {
		Funcs    []uint32
		Globals  int
		Tables   int
		Memories int
	}
}

// Custom returns a custom section with a specific name, if it exists.
func (m *Module) Custom(name string) *SectionCustom {
	for _, s := range m.Customs {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (m *Module) AddHostFunc(name string, fn interface{}) {
	val := reflect.ValueOf(fn)
	typ := val.Type()
	if typ.Kind() != reflect.Func {
		panic(fmt.Errorf("AddHostFunc expects a function, got %s", typ.Kind()))
	}
	// FIXME: gross, but there is an import cycle with exec.
	offset := 0
	firstParamIsProcess := typ.NumIn() > 0 && typ.In(0).String() == "*exec.Process"
	if firstParamIsProcess {
		offset = 1
	}
	hostFn := Function{Host: val, Sig: &FunctionSig{Form: 0}}
	if name != "" && typ.IsVariadic() {
		panic(fmt.Errorf("AddHostFunc only supports variadic functions for the \"\" undefined function handler"))
	}
	if !typ.IsVariadic() {
		paramTypes := make([]ValueType, typ.NumIn()-offset)
		for i := 0; i < len(paramTypes); i++ {
			paramTypes[i] = reflectToValueType[typ.In(i+offset).Kind()]
		}
		returnTypes := make([]ValueType, typ.NumOut())
		for i := 0; i < len(returnTypes); i++ {
			returnTypes[i] = reflectToValueType[typ.Out(i).Kind()]
		}
		hostFn.Sig.ParamTypes = paramTypes
		hostFn.Sig.ReturnTypes = returnTypes
	}
	index := uint32(len(m.FunctionIndexSpace))
	m.FunctionIndexSpace = append(m.FunctionIndexSpace, hostFn)
	m.Types.Entries = append(m.Types.Entries, *hostFn.Sig)
	if m.Export.Entries == nil {
		m.Export.Entries = map[string]ExportEntry{}
	}
	m.Export.Entries[name] = ExportEntry{FieldStr: name, Kind: ExternalFunction, Index: index}
}

// NewModule creates a new empty module
func NewModule() *Module {
	return &Module{
		Types:    &SectionTypes{},
		Import:   &SectionImports{},
		Table:    &SectionTables{},
		Memory:   &SectionMemories{},
		Global:   &SectionGlobals{},
		Export:   &SectionExports{},
		Start:    &SectionStartFunction{},
		Elements: &SectionElements{},
		Data:     &SectionData{},
	}
}

// ResolveFunc is a function that takes a module name and
// returns a valid resolved module.
type ResolveFunc func(name string) (*Module, error)

// DecodeModule is the same as ReadModule, but it only decodes the module without
// initializing the index space or resolving imports.
func DecodeModule(r io.Reader) (*Module, error) {
	reader := &readpos.ReadPos{
		R:      r,
		CurPos: 0,
	}
	m := &Module{}
	magic, err := readU32(reader)
	if err != nil {
		return nil, err
	}
	if magic != Magic {
		return nil, ErrInvalidMagic
	}
	if m.Version, err = readU32(reader); err != nil {
		return nil, err
	}

	for {
		done, err := m.readSection(reader)
		if err != nil {
			return nil, err
		} else if done {
			return m, nil
		}
	}
}

// ReadModule reads a module from the reader r. resolvePath must take a string
// and a return a reader to the module pointed to by the string.
func ReadModule(r io.Reader, resolvePath ResolveFunc) (*Module, error) {
	m, err := DecodeModule(r)
	if err != nil {
		return nil, err
	}

	m.LinearMemoryIndexSpace = make([][]byte, 1)
	if m.Table != nil {
		m.TableIndexSpace = make([][]uint32, int(len(m.Table.Entries)))
	}

	if m.Import != nil && resolvePath != nil {
		if m.Code == nil {
			m.Code = &SectionCode{}
		}

		err := m.resolveImports(resolvePath)
		if err != nil {
			return nil, err
		}
	}

	for _, fn := range []func() error{
		m.populateGlobals,
		m.populateFunctions,
		m.populateTables,
		m.populateLinearMemory,
	} {
		if err := fn(); err != nil {
			return nil, err
		}

	}

	logger.Printf("There are %d entries in the function index space.", len(m.FunctionIndexSpace))
	return m, nil
}
