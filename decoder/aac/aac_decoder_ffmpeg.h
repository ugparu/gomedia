#ifndef _AAC_DECODER_FFMPEG_H_
#define _AAC_DECODER_FFMPEG_H_

#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libavutil/samplefmt.h>
#include <libavutil/channel_layout.h>
#include <libavutil/opt.h>
#include <libavutil/mem.h>
#include <libswresample/swresample.h>
#include <stdlib.h>

#ifndef MAX_AUDIO_FRAME_SIZE
#define MAX_AUDIO_FRAME_SIZE 192000 // 1 second of 48khz 32bit stereo
#endif

typedef struct {
    AVCodecContext *codec_ctx;
    AVFrame *frame;
    AVPacket *packet;
    SwrContext *swr_ctx;
    uint8_t *audio_buf;
    int audio_buf_size;
    int audio_buf_index;
    
    // Output format parameters
    int out_sample_rate;
    int out_channels;
    enum AVSampleFormat out_sample_fmt;
    uint64_t out_channel_layout;
} aacDecoder;

int init_aac_decoder(aacDecoder *dec, AVCodecParameters *par);
int decode_aac_packet(aacDecoder *dec, uint8_t **output, int *output_size);
int flush_aac_decoder(aacDecoder *dec, uint8_t **output, int *output_size);
void reset_aac_decoder(aacDecoder *dec);
void close_aac_decoder(aacDecoder *dec);

#endif
