package buffer

import "io"

type RefBuffer interface {
	io.ReadWriter
	Data() []byte
	Len() int
	Cap() int
	AddRef()
	Resize(int)
	Close()
}
