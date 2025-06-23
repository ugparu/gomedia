#ifndef _DECODER_FFMPEG_CPU_H_
#define _DECODER_FFMPEG_H_

#include <libavcodec/avcodec.h>
#include <libswscale/swscale.h>

#ifndef MAX_WIDTH
#define MAX_WIDTH 4096
#endif

#ifndef MAX_HEIGHT
#define MAX_HEIGHT 3072
#endif

typedef struct {
    AVCodecContext *ctxt;
    AVFrame *frame;
    AVPacket *packet;
    struct SwsContext *scale_ctxt;
    AVFrame *rgb_frame;
} cpuDecoder;

int init_cpu_decoder(cpuDecoder *dec, AVCodecParameters *par);
int decode_cpu_packet(cpuDecoder *dec, uint8_t *buffer);
void close_cpu_decoder(cpuDecoder *dec);

#endif
