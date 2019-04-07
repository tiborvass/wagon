package wasm

import (
	"bytes"
	"fmt"

	"github.com/go-interpreter/wagon/wasm/leb128"
)

type CustomName struct {
	Index uint32
	Name  string
}

func CustomReadNames(b []byte) ([]CustomName, error) {
	typ := b[0]
	if typ != 1 {
		panic(fmt.Sprintf("unsupported: %d", typ))
	}
	r := bytes.NewReader(b[1:])
	totalSize, err := leb128.ReadVarUint32(r)
	if err != nil {
		return nil, err
	}
	// FIXME check totalSize: need to change func signature to readpos.Reader
	_ = totalSize
	n, err := leb128.ReadVarUint32(r)
	if err != nil {
		return nil, err
	}
	names := make([]CustomName, n)
	for i := range names {
		names[i].Index, err = leb128.ReadVarUint32(r)
		if err != nil {
			return nil, err
		}
		names[i].Name, err = readStringUint(r)
		if err != nil {
			return nil, err
		}
	}
	return names, nil
}
