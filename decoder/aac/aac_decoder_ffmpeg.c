#include <libavutil/opt.h>
#include <libavutil/channel_layout.h>
#include <libavutil/mem.h>
#include "aac_decoder_ffmpeg.h"

int init_aac_decoder(aacDecoder *dec, AVCodecParameters *par) {
    // Initialize all pointers to NULL for safe cleanup
    dec->packet = NULL;
    dec->frame = NULL;
    dec->codec_ctx = NULL;
    dec->swr_ctx = NULL;
    dec->audio_buf = NULL;
    dec->audio_buf_index = 0;

    // Allocate packet and frame
    dec->packet = av_packet_alloc();
    dec->frame = av_frame_alloc();
    if (!dec->packet || !dec->frame) {
        close_aac_decoder(dec);
        return AVERROR(ENOMEM);
    }

    // Find the AAC decoder
    const AVCodec *codec = avcodec_find_decoder(par->codec_id);
    if (!codec) {
        close_aac_decoder(dec);
        return AVERROR_DECODER_NOT_FOUND;
    }

    // Allocate codec context
    dec->codec_ctx = avcodec_alloc_context3(codec);
    if (!dec->codec_ctx) {
        close_aac_decoder(dec);
        return AVERROR(ENOMEM);
    }

    // Copy codec parameters to context
    int ret = avcodec_parameters_to_context(dec->codec_ctx, par);
    if (ret < 0) {
        close_aac_decoder(dec);
        return ret;
    }

    // Set decoding parameters for low latency
    dec->codec_ctx->flags |= AV_CODEC_FLAG_LOW_DELAY;
    dec->codec_ctx->flags2 |= AV_CODEC_FLAG2_FAST;

    // Open codec
    ret = avcodec_open2(dec->codec_ctx, codec, NULL);
    if (ret < 0) {
        close_aac_decoder(dec);
        return ret;
    }

    // Set up output format - we want 16-bit signed PCM
    dec->out_sample_rate = dec->codec_ctx->sample_rate;
    dec->out_channels = dec->codec_ctx->ch_layout.nb_channels;
    dec->out_sample_fmt = AV_SAMPLE_FMT_S16;
    
    // Set up channel layout
    if (dec->codec_ctx->ch_layout.nb_channels == 1) {
        av_channel_layout_default(&dec->codec_ctx->ch_layout, 1);
    } else if (dec->codec_ctx->ch_layout.nb_channels == 2) {
        av_channel_layout_default(&dec->codec_ctx->ch_layout, 2);
    }

    // Initialize sample rate converter for format conversion
    ret = swr_alloc_set_opts2(&dec->swr_ctx,
                              &dec->codec_ctx->ch_layout, dec->out_sample_fmt, dec->out_sample_rate,
                              &dec->codec_ctx->ch_layout, dec->codec_ctx->sample_fmt, dec->codec_ctx->sample_rate,
                              0, NULL);
    if (ret < 0) {
        close_aac_decoder(dec);
        return ret;
    }

    ret = swr_init(dec->swr_ctx);
    if (ret < 0) {
        close_aac_decoder(dec);
        return ret;
    }

    // Allocate audio buffer
    dec->audio_buf_size = MAX_AUDIO_FRAME_SIZE;
    dec->audio_buf = (uint8_t*)av_malloc(dec->audio_buf_size);
    if (!dec->audio_buf) {
        close_aac_decoder(dec);
        return AVERROR(ENOMEM);
    }

    return 0;
}

int decode_aac_packet(aacDecoder *dec, uint8_t **output, int *output_size) {
    *output = NULL;
    *output_size = 0;

    // Send packet to decoder
    int ret = avcodec_send_packet(dec->codec_ctx, dec->packet);
    av_packet_unref(dec->packet);
    if (ret < 0) {
        if (ret == AVERROR_EOF || ret == AVERROR_INVALIDDATA) {
            return 1; // Need more data
        }
        return ret;
    }

    // Receive frame from decoder
    ret = avcodec_receive_frame(dec->codec_ctx, dec->frame);
    if (ret < 0) {
        if (ret == AVERROR(EAGAIN) || ret == AVERROR_INVALIDDATA) {
            return 1; // Need more data
        }
        return ret;
    }

    // Calculate output buffer size
    int out_samples = swr_get_out_samples(dec->swr_ctx, dec->frame->nb_samples);
    int out_size = av_samples_get_buffer_size(NULL, dec->out_channels, out_samples, dec->out_sample_fmt, 1);
    
    if (out_size > dec->audio_buf_size) {
        // Reallocate buffer if needed
        uint8_t* new_buf = (uint8_t*)av_realloc(dec->audio_buf, out_size);
        if (!new_buf) {
            // Keep original buffer on failure
            return AVERROR(ENOMEM);
        }
        dec->audio_buf = new_buf;
        dec->audio_buf_size = out_size;
    }

    // Convert audio to desired format
    int converted_samples = swr_convert(dec->swr_ctx, &dec->audio_buf, out_samples,
                                        (const uint8_t**)dec->frame->data, dec->frame->nb_samples);
    
    // Unref the frame after processing to prevent memory accumulation
    av_frame_unref(dec->frame);
    
    if (converted_samples < 0) {
        return converted_samples;
    }

    // Calculate actual output size
    *output_size = av_samples_get_buffer_size(NULL, dec->out_channels, converted_samples, dec->out_sample_fmt, 1);
    *output = dec->audio_buf;

    return 0;
}

int flush_aac_decoder(aacDecoder *dec, uint8_t **output, int *output_size) {
    *output = NULL;
    *output_size = 0;

    // Send NULL packet to flush decoder
    int ret = avcodec_send_packet(dec->codec_ctx, NULL);
    if (ret < 0) {
        return ret;
    }

    // Try to receive any remaining frames
    ret = avcodec_receive_frame(dec->codec_ctx, dec->frame);
    if (ret < 0) {
        if (ret == AVERROR_EOF || ret == AVERROR(EAGAIN)) {
            return 1; // No more frames
        }
        return ret;
    }

    // Convert the frame like in normal decode
    int out_samples = swr_get_out_samples(dec->swr_ctx, dec->frame->nb_samples);
    int out_size = av_samples_get_buffer_size(NULL, dec->out_channels, out_samples, dec->out_sample_fmt, 1);
    
    if (out_size > dec->audio_buf_size) {
        uint8_t* new_buf = (uint8_t*)av_realloc(dec->audio_buf, out_size);
        if (!new_buf) {
            // Keep original buffer on failure
            return AVERROR(ENOMEM);
        }
        dec->audio_buf = new_buf;
        dec->audio_buf_size = out_size;
    }

    int converted_samples = swr_convert(dec->swr_ctx, &dec->audio_buf, out_samples,
                                        (const uint8_t**)dec->frame->data, dec->frame->nb_samples);
    
    // Unref the frame after processing to prevent memory accumulation
    av_frame_unref(dec->frame);
    
    if (converted_samples < 0) {
        return converted_samples;
    }

    *output_size = av_samples_get_buffer_size(NULL, dec->out_channels, converted_samples, dec->out_sample_fmt, 1);
    *output = dec->audio_buf;

    return 0;
}

void reset_aac_decoder(aacDecoder *dec) {
    if (!dec || !dec->codec_ctx) {
        return;
    }
    
    // Flush any remaining frames in the decoder
    avcodec_flush_buffers(dec->codec_ctx);
    
    // Reset packet and frame state
    if (dec->packet) {
        av_packet_unref(dec->packet);
    }
    if (dec->frame) {
        av_frame_unref(dec->frame);
    }
}

void close_aac_decoder(aacDecoder *dec) {
    if (!dec) {
        return;
    }

    if (dec->codec_ctx) {
        avcodec_free_context(&dec->codec_ctx);
    }
    if (dec->packet) {
        av_packet_free(&dec->packet);
    }
    if (dec->frame) {
        av_frame_free(&dec->frame);
    }
    if (dec->swr_ctx) {
        swr_free(&dec->swr_ctx);
    }
    if (dec->audio_buf) {
        av_free(dec->audio_buf);
        dec->audio_buf = NULL;
    }
}
