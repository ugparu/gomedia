//go:build !unix

package buffer

func mmapBytes(size int) ([]byte, error) {
	return make([]byte, size), nil
}

func setMmapFinalizer(_ *RingAlloc) {}
