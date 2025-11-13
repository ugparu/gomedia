#ifndef _DECODER_FFMPEG_RKMPP_H_
#define _DECODER_FFMPEG_RKMPP_H_

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
} rkmppDecoder;

int init_rkmpp_decoder(rkmppDecoder *dec, AVCodecParameters *par);
int decode_rkmpp_packet(rkmppDecoder *dec, uint8_t *buffer);
void close_rkmpp_decoder(rkmppDecoder *dec);

#endif

