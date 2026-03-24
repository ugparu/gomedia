package buffer

//go:generate mockgen -source=interface.go -destination=../../mocks/mock_buffer.go -package=mocks

type Buffer interface {
	Data() []byte

	Len() int
	Cap() int

	Resize(int)
}
