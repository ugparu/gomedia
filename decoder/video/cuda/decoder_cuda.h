#ifndef _DECODER_CUDA_H_
#define _DECODER_CUDA_H_

#include <libavcodec/avcodec.h>
#include <cuda_runtime.h>
#include <nppi.h>

#ifndef MAX_WIDTH
#define MAX_WIDTH 4096
#endif

#ifndef MAX_HEIGHT
#define MAX_HEIGHT 3072
#endif

#define MAX_MATS_COUNT 100

typedef struct {
    AVCodecContext *ctxt;
    AVFrame *frame;
    AVPacket *packet;
    int mat_index;
    int npp_step;
    int frame_width;
    int frame_height;
    cudaStream_t stream;
    NppStreamContext npp_stream_ctx;
} cudaDecoder;


int init_cuda_device();
void close_cuda_device();

int init_cuda_decoder(cudaDecoder *dec, AVCodecParameters *par);
int decode_cuda_packet(cudaDecoder *dec, uint8_t *buffer);
void close_cuda_decoder(cudaDecoder *dec);

#endif
