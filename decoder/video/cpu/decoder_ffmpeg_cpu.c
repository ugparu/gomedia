#include <libavutil/imgutils.h>
#include "decoder_ffmpeg_cpu.h"

int init_cpu_decoder(cpuDecoder *dec, AVCodecParameters *par) {
    dec->packet = av_packet_alloc();
    dec->frame = av_frame_alloc();

    dec->ctxt = avcodec_alloc_context3(avcodec_find_decoder(par->codec_id));
    int ret = avcodec_parameters_to_context(dec->ctxt, par);
    if (ret < 0) {
        return ret;
    }

    dec->ctxt->flags &= AV_CODEC_FLAG_LOW_DELAY;
	dec->ctxt->flags2 &= AV_CODEC_FLAG2_FAST;

	dec->ctxt->pkt_timebase.num = 1;
	dec->ctxt->pkt_timebase.den = 1000000;

	ret = avcodec_open2(dec->ctxt, dec->ctxt->codec, NULL);
    if (ret < 0) {
        return ret;
    }

    ret = avcodec_parameters_from_context(par, dec->ctxt);
    if (ret < 0) {
        return ret;
    }

    int width = par->width;
    int height = par->height;

	float wScale = (float)(MAX_WIDTH) / (float)(width);
	float hScale = (float)(MAX_HEIGHT) / (float)(height);
	float scale = wScale;
    if (hScale < scale) {
        scale = hScale;
    } 
	if (scale < 1) {
		width = scale * width;
		height = scale * height;
	}

    dec->scale_ctxt = sws_getContext(par->width, par->height, par->format, width, height, 
                                     AV_PIX_FMT_RGB24, SWS_FAST_BILINEAR, NULL, NULL, NULL);

    dec->rgb_frame = av_frame_alloc();
	dec->rgb_frame->width = width;
	dec->rgb_frame->height = height;
	dec->rgb_frame->format = AV_PIX_FMT_RGB24;

    ret = av_frame_get_buffer(dec->rgb_frame, 0);
	if (ret < 0) {
		return ret;
	}

    return 0;
}

int decode_cpu_packet(cpuDecoder *dec, uint8_t *buffer) {
    int ret = avcodec_send_packet(dec->ctxt, dec->packet);
    av_packet_unref(dec->packet);
    if (ret < 0) {
        if (ret == AVERROR_EOF || ret == AVERROR_INVALIDDATA) {
            return 1;
        }
        return ret;
    }

    ret = avcodec_receive_frame(dec->ctxt, dec->frame);
    if (ret < 0) {
        if (ret == -11 || ret == AVERROR_INVALIDDATA) {
            return 1;
        }
        return ret;
    }

    if (!buffer) {
        return 0;
    }

    dec->frame->pts = dec->frame->best_effort_timestamp;
    ret = sws_scale_frame(dec->scale_ctxt, dec->rgb_frame, dec->frame);

    if (ret<0) {
        return ret;
    }

    ret = av_image_copy_to_buffer(buffer, dec->rgb_frame->width*dec->rgb_frame->height*3, 
        (const uint8_t * const*)dec->rgb_frame->data, (const int*)dec->rgb_frame->linesize,
		dec->rgb_frame->format, dec->rgb_frame->width, dec->rgb_frame->height, 1);
    if (ret < 0) {
        return ret;
    }

    return 0;
}


void close_cpu_decoder(cpuDecoder *dec) {
    if (!dec) {
        return;
    }
    
    if (dec->ctxt) {
        avcodec_free_context(&dec->ctxt);
    }
    if (dec->packet) {
        av_packet_free(&dec->packet);
    }
    if (dec->frame) {
        av_frame_free(&dec->frame);
    }
    if (dec->scale_ctxt) {
        sws_freeContext(dec->scale_ctxt);
    }
    if (dec->rgb_frame) {
        av_frame_free(&dec->rgb_frame);
    }
}
