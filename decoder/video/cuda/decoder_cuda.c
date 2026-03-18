#include <libavutil/dict.h>
#include <libavutil/avutil.h>
#include "decoder_cuda.h"

Npp8u *mats[MAX_MATS_COUNT];
int    mat_steps[MAX_MATS_COUNT];
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

void close_cuda_device() {
    for (int i = 0; i < MAX_MATS_COUNT; i++) {
        if (mats[i]) {
            nppiFree(mats[i]);
            mats[i] = NULL;
            mat_steps[i] = 0;
        }
    }
    if (decoder_cuda_buffer) {
        av_buffer_unref(&decoder_cuda_buffer);
    }
}

int init_cuda_decoder(cudaDecoder *dec, AVCodecParameters *par) {
    if (par->width > MAX_WIDTH || par->height > MAX_HEIGHT) {
        return -1;
    }
    
    dec->packet = av_packet_alloc();
    if (!dec->packet) {
        return AVERROR(ENOMEM);
    }
    dec->frame = av_frame_alloc();
    if (!dec->frame) {
        return AVERROR(ENOMEM);
    }

    const AVCodec *codec = NULL;
    if (par->codec_id == AV_CODEC_ID_H264) {
        codec = avcodec_find_decoder_by_name("h264_cuvid");
    } else if (par->codec_id == AV_CODEC_ID_HEVC) {
        codec = avcodec_find_decoder_by_name("hevc_cuvid");
    } else {
        return AVERROR(EINVAL);
    }
    if (!codec) {
        return AVERROR_DECODER_NOT_FOUND;
    }

    dec->ctxt = avcodec_alloc_context3(codec);
    if (!dec->ctxt) {
        return AVERROR(ENOMEM);
    }

    int ret = avcodec_parameters_to_context(dec->ctxt, par);
    if (ret < 0) {
        return ret;
    }

    dec->ctxt->flags |= AV_CODEC_FLAG_LOW_DELAY;
	dec->ctxt->flags2 |= AV_CODEC_FLAG2_FAST;

	dec->ctxt->pkt_timebase.num = 1;
	dec->ctxt->pkt_timebase.den = 1000000;

    dec->ctxt->hw_device_ctx = av_buffer_ref(decoder_cuda_buffer);

	dec->ctxt->hwaccel_flags |= AV_HWACCEL_FLAG_IGNORE_LEVEL;
	dec->ctxt->hwaccel_flags |= AV_HWACCEL_FLAG_ALLOW_PROFILE_MISMATCH;
	dec->ctxt->extra_hw_frames = 8;
	
	ret = avcodec_open2(dec->ctxt, codec, NULL);
    if (ret < 0) {
        return ret;
    }

    ret = avcodec_parameters_from_context(par, dec->ctxt);
    if (ret < 0) {
        return ret;
    }

    dec->frame_width  = par->width;
    dec->frame_height = par->height;

    cudaError_t cerr = cudaStreamCreate(&dec->stream);
    if (cerr != cudaSuccess) {
        return AVERROR_EXTERNAL;
    }

    struct cudaDeviceProp props;
    int device_id;
    cudaGetDevice(&device_id);
    cudaGetDeviceProperties(&props, device_id);

    dec->npp_stream_ctx.hStream                         = dec->stream;
    dec->npp_stream_ctx.nCudaDeviceId                   = device_id;
    dec->npp_stream_ctx.nMultiProcessorCount            = props.multiProcessorCount;
    dec->npp_stream_ctx.nMaxThreadsPerMultiProcessor    = props.maxThreadsPerMultiProcessor;
    dec->npp_stream_ctx.nMaxThreadsPerBlock             = props.maxThreadsPerBlock;
    dec->npp_stream_ctx.nSharedMemPerBlock              = props.sharedMemPerBlock;
    cudaDeviceGetAttribute(&dec->npp_stream_ctx.nCudaDevAttrComputeCapabilityMajor, cudaDevAttrComputeCapabilityMajor, device_id);
    cudaDeviceGetAttribute(&dec->npp_stream_ctx.nCudaDevAttrComputeCapabilityMinor, cudaDevAttrComputeCapabilityMinor, device_id);
    cudaStreamGetFlags(dec->stream, &dec->npp_stream_ctx.nStreamFlags);

    if (!mats[dec->mat_index]) {
        mats[dec->mat_index] = nppiMalloc_8u_C3(MAX_WIDTH, MAX_HEIGHT, &mat_steps[dec->mat_index]);
    }
    dec->npp_step = mat_steps[dec->mat_index];

    return 0;
}

int decode_cuda_packet(cudaDecoder *dec, uint8_t *buffer) {
    int ret = avcodec_send_packet(dec->ctxt, dec->packet);
    av_packet_unref(dec->packet);
    if (ret < 0) {
        if (ret == AVERROR_EOF || ret == AVERROR_INVALIDDATA || ret == AVERROR(EAGAIN)) {
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

    dec->frame_width  = dec->frame->width;
    dec->frame_height = dec->frame->height;

    if (!buffer) {
        av_frame_unref(dec->frame);
        return 0;
    }

    NppiSize sz;
    sz.width = dec->frame->width;
    sz.height = dec->frame->height;

    ret = nppiNV12ToRGB_8u_P2C3R_Ctx((const Npp8u * const*)dec->frame->data, dec->frame->linesize[0], mats[dec->mat_index], dec->npp_step, sz, dec->npp_stream_ctx);
    if (ret != NPP_SUCCESS) {
        av_frame_unref(dec->frame);
        return -999;
    }

    cudaError_t err = cudaMemcpy2DAsync(buffer, dec->frame->width * 3, mats[dec->mat_index], dec->npp_step, dec->frame->width * 3, dec->frame->height, cudaMemcpyDeviceToHost, dec->npp_stream_ctx.hStream);
    av_frame_unref(dec->frame);
    if (err != cudaSuccess) {
        return -999;
    }

    err = cudaStreamSynchronize(dec->npp_stream_ctx.hStream);
    if (err != cudaSuccess) {
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
    if (dec->stream) {
        cudaStreamDestroy(dec->stream);
        dec->stream = NULL;
    }
}
