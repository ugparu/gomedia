package buffer

import "io"

type RefBuffer interface {
	io.ReadWriter
	Data() []byte
	AddRef()
	Resize(int)
	Close()
}
