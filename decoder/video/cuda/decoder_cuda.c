#include <libavutil/dict.h>
#include <libavutil/avutil.h>
#include "decoder_cuda.h"

Npp8u *mats[MAX_MATS_COUNT];
AVBufferRef *decoder_cuda_buffer = NULL;

int init_cuda_device() {
    AVDictionary *dict = NULL;

	int ret = av_dict_set_int(&dict, "primary_ctx", 1, 0);
    if (ret < 0) {
        return ret;
    }

    ret = av_hwdevice_ctx_create(&decoder_cuda_buffer, AV_HWDEVICE_TYPE_CUDA, "0", dict, 0);
    av_dict_free(&dict);
    if (ret < 0) {
        return ret;
    }

    return ret;
}

int init_cuda_decoder(cudaDecoder *dec, AVCodecParameters *par) {
    if (par->width > MAX_WIDTH || par->height > MAX_HEIGHT) {
        return -1;
    }
    
    dec->packet = av_packet_alloc();
    dec->frame = av_frame_alloc();

    if (par->codec_id == AV_CODEC_ID_H264) {
        dec->ctxt = avcodec_alloc_context3(avcodec_find_decoder_by_name("h264_cuvid"));
    } else if (par->codec_id == AV_CODEC_ID_HEVC) {
        dec->ctxt = avcodec_alloc_context3(avcodec_find_decoder_by_name("hevc_cuvid"));
    } else {
        return -11;
    }
    int ret = avcodec_parameters_to_context(dec->ctxt, par);
    if (ret < 0) {
        return ret;
    }

    dec->ctxt->flags &= AV_CODEC_FLAG_LOW_DELAY;
	dec->ctxt->flags2 &= AV_CODEC_FLAG2_FAST;

	dec->ctxt->pkt_timebase.num = 1;
	dec->ctxt->pkt_timebase.den = 1000000;

    dec->ctxt->hw_device_ctx = av_buffer_ref(decoder_cuda_buffer);

	dec->ctxt->hwaccel_flags &= AV_HWACCEL_FLAG_IGNORE_LEVEL;
	dec->ctxt->hwaccel_flags &= AV_HWACCEL_FLAG_ALLOW_PROFILE_MISMATCH;
	dec->ctxt->extra_hw_frames = 8;
	
	ret = avcodec_open2(dec->ctxt, dec->ctxt->codec, NULL);
    if (ret < 0) {
        return ret;
    }

    ret = avcodec_parameters_from_context(par, dec->ctxt);
    if (ret < 0) {
        return ret;
    }

    if (!mats[dec->mat_index]) {
        int dummy;
        mats[dec->mat_index] = nppiMalloc_8u_C3(MAX_WIDTH, MAX_HEIGHT, &dummy);
    } 

    return 0;
}

int decode_cuda_packet(cudaDecoder *dec, uint8_t *buffer) {
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

    NppiSize sz;
    sz.width = dec->frame->width;
    sz.height = dec->frame->height;

    ret = nppiNV12ToRGB_8u_P2C3R((const Npp8u * const*)dec->frame->data, dec->frame->linesize[0], mats[dec->mat_index], dec->frame->width * 3, sz);
    if (ret != NPP_SUCCESS) {
        return -999; 
    }

    cudaError_t err = cudaMemcpy2D(buffer, dec->frame->width * 3, mats[dec->mat_index], dec->frame->width * 3, dec->frame->width * 3, dec->frame->height, cudaMemcpyDefault);
    if (err < 0) {
        return -999; 
    }
    
    return 0;
}

void close_cuda_decoder(cudaDecoder *dec) {
    if (dec->ctxt) {
        avcodec_free_context(&dec->ctxt);
    }
    if (dec->packet) {
        av_packet_free(&dec->packet);
    }
    if (dec->frame) {
        av_frame_free(&dec->frame);
    }
}
