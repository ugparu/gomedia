package buffer

type PooledBuffer interface {
	Data() []byte

	Len() int
	Cap() int

	// Release returns the buffer to the pool. After calling Release,
	// the buffer should not be used.
	Release()

	Resize(int)
}
