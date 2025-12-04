package buffer

type PooledBuffer interface {
	Data() []byte

	Len() int
	Cap() int

	Retain()
	Release()

	Resize(int)
}
