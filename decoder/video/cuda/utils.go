package cuda

//#cgo LDFLAGS: -L/usr/local/cuda/lib64 -lnppc -lnppial -lnppicc -lnppidei -lnppif -lnppig -lnppim -lnppist -lnppisu -lnppitc -lnpps -lcudart
//#cgo CFLAGS: -I/usr/local/cuda/include
//#include "decoder_cuda.h"
import "C"

var cudaMatIdxs chan int
var freeMatIdxs chan int

func CheckCuda() bool {
	return C.init_cuda_device() == 0
}

func InitCuda(maxMats int) {
	cudaMatIdxs = make(chan int, maxMats)
	freeMatIdxs = make(chan int, maxMats)

	for i := range maxMats {
		freeMatIdxs <- i
	}
}
