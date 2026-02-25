//go:build linux && arm64
/* SPDX-License-Identifier: Apache-2.0 OR MIT */

#include <errno.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <rga/im2d.h>
#include <rga/RgaApi.h>

#include "decoder_rkmpp_native.h"

static MppCodingType codec_id_to_mpp(int codec_id)
{
    switch (codec_id) {
    case 1:
        return MPP_VIDEO_CodingAVC;
    case 2:
        return MPP_VIDEO_CodingHEVC;
    default:
        return MPP_VIDEO_CodingUnused;
    }
}


static rga_buffer_handle_t get_src_handle(NativeRkmppDecoder *dec,
    int fd,
    int hor_stride,
    int ver_stride,
    int rga_fmt)
{
    for (int i = 0; i < dec->cache_count; i++) {
        if (dec->cached_fds[i] == fd)
            return dec->cached_handles[i];
    }

    im_handle_param_t param;
    memset(&param, 0, sizeof(param));
    param.width  = hor_stride;
    param.height = ver_stride;
    param.format = rga_fmt;

    rga_buffer_handle_t handle = importbuffer_fd(fd, &param);
    if (!handle)
        return 0;

    if (dec->cache_count < 24) {
        dec->cached_fds[dec->cache_count]    = fd;
        dec->cached_handles[dec->cache_count] = handle;
        dec->cache_count++;
    }

    return handle;
}

int rga_nv12_to_rgb(NativeRkmppDecoder *dec,
                    MppFrame frame,
                    uint8_t *dst_buffer,
                    int dst_width,
                    int dst_height)
{
    if (!dec || !frame || !dst_buffer)
        return -1;

    MppBuffer buf = mpp_frame_get_buffer(frame);
    if (!buf)
        return -2;

    int fd = mpp_buffer_get_fd(buf);
    if (fd <= 0)
        return -3;

    RK_U32 width      = mpp_frame_get_width(frame);
    RK_U32 height     = mpp_frame_get_height(frame);
    RK_U32 hor_stride = mpp_frame_get_hor_stride(frame);
    RK_U32 ver_stride = mpp_frame_get_ver_stride(frame);
    MppFrameFormat fmt = mpp_frame_get_fmt(frame);

    int rga_fmt;
    if (fmt == MPP_FMT_YUV420SP) {
        rga_fmt = RK_FORMAT_YCbCr_420_SP;
    } else if (fmt == MPP_FMT_YUV420SP_VU) {
        rga_fmt = RK_FORMAT_YCrCb_420_SP;
    } else {
        return -4;
    }

    if (dst_width <= 0 || dst_height <= 0)
        return -5;

    if (!dec->dst_handle || !dec->dst_rgb_buf)
        return -11;

    rga_buffer_handle_t src_handle = get_src_handle(dec, fd, hor_stride, ver_stride, rga_fmt);
    if (!src_handle)
        return -12;

    rga_buffer_t src_img = wrapbuffer_handle_t(src_handle,
                                               (int)width, (int)height,
                                               (int)hor_stride, (int)ver_stride,
                                               rga_fmt);
    rga_buffer_t dst_img = wrapbuffer_handle_t(dec->dst_handle,
                                               dst_width, dst_height,
                                               dec->dst_wstride, dst_height,
                                               RK_FORMAT_RGB_888);

    IM_STATUS status = imcvtcolor_t(src_img, dst_img,
                                    rga_fmt, RK_FORMAT_RGB_888,
                                    IM_COLOR_SPACE_DEFAULT, IM_SYNC);

    if (status != IM_STATUS_SUCCESS)
        return -6;

    /* Copy from aligned internal buffer to caller-provided buffer */
    if (dec->dst_wstride == dst_width) {
        memcpy(dst_buffer, dec->dst_rgb_buf, dst_width * dst_height * 3);
    } else {
        int src_row_bytes = dec->dst_wstride * 3;
        int dst_row_bytes = dst_width * 3;
        for (int y = 0; y < dst_height; y++)
            memcpy(dst_buffer + y * dst_row_bytes,
                   dec->dst_rgb_buf + y * src_row_bytes,
                   dst_row_bytes);
    }

    return 0;
}

int init_rkmpp_decoder_native(NativeRkmppDecoder *dec,
                              int codec_id,
                              int width,
                              int height)
{
    MPP_RET ret;
    MppCtx ctx = NULL;
    MppApi *mpi = NULL;
    MppDecCfg cfg = NULL;
    MppCodingType type = codec_id_to_mpp(codec_id);

    if (!dec)
        return -1;

    memset(dec, 0, sizeof(*dec));

    if (type == MPP_VIDEO_CodingUnused)
        return -2;

    dec->width = width;
    dec->height = height;
    dec->coding_type = type;

    ret = mpp_create(&ctx, &mpi);
    if (ret) {
        return -3;
    }

    ret = mpp_init(ctx, MPP_CTX_DEC, type);
    if (ret) {
        mpp_destroy(ctx);
        return -4;
    }

    ret = mpp_dec_cfg_init(&cfg);
    if (ret) {
        mpp_destroy(ctx);
        return -5;
    }

    ret = mpi->control(ctx, MPP_DEC_GET_CFG, cfg);
    if (ret) {
        mpp_dec_cfg_deinit(cfg);
        mpp_destroy(ctx);
        return -6;
    }

    /* Disable internal frame splitter - we will split NAL units ourselves */
    ret = mpp_dec_cfg_set_u32(cfg, "base:split_parse", 1);
    if (ret) {
        mpp_dec_cfg_deinit(cfg);
        mpp_destroy(ctx);
        return -7;
    }

    ret = mpi->control(ctx, MPP_DEC_SET_CFG, cfg);
    if (ret) {
        mpp_dec_cfg_deinit(cfg);
        mpp_destroy(ctx);
        return -8;
    }

    dec->ctx = ctx;
    dec->mpi = mpi;
    dec->cfg = cfg;
    dec->frm_grp = NULL;
    dec->frame = NULL;
    dec->eos_reached = 0;

    return 0;
}

int feed_rkmpp_packet_native(NativeRkmppDecoder *dec,
                             const uint8_t *data,
                             int size,
                             int64_t pts_ms)
{
    MPP_RET ret;
    MppPacket packet = NULL;

    if (!dec || !dec->ctx || !dec->mpi)
        return -1;

    if (!data || size <= 0)
        return 1;

    ret = mpp_packet_init(&packet, (void *)data, size);
    if (ret) {
        return -2;
    }

    mpp_packet_set_pos(packet, (void *)data);
    mpp_packet_set_length(packet, size);
    mpp_packet_set_pts(packet, pts_ms * 1000); /* microseconds */

    ret = dec->mpi->decode_put_packet(dec->ctx, packet);
    mpp_packet_deinit(&packet);

    if (ret == MPP_ERR_BUFFER_FULL) {
        /* try again after consuming frames */
        return 2;
    }

    if (ret) {
        return -3;
    }

    return 0;
}

int decode_rkmpp_frame_native(NativeRkmppDecoder *dec,
                              uint8_t *rgb_buffer,
                              int rgb_buf_size)
{
    MPP_RET ret;
    MppFrame frame = NULL;
    int try_times = 30;

    if (!dec || !dec->ctx || !dec->mpi)
        return -1;

    if (!rgb_buffer || rgb_buf_size < dec->width * dec->height * 3)
        return -2;

    /* Try to get one frame */
try_again:
    ret = dec->mpi->decode_get_frame(dec->ctx, &frame);
    if (ret == MPP_ERR_TIMEOUT && try_times-- > 0) {
        usleep(1000);
        goto try_again;
    }
    if (ret) {
        return -3;
    }

    if (!frame)
        return 1;

    if (mpp_frame_get_info_change(frame)) {
        RK_U32 width    = mpp_frame_get_width(frame);
        RK_U32 height   = mpp_frame_get_height(frame);
        RK_U32 buf_size = mpp_frame_get_buf_size(frame);
        MppBufferGroup grp = NULL;

        if (!dec->frm_grp) {
            ret = mpp_buffer_group_get_internal(&grp, MPP_BUFFER_TYPE_ION);
            if (ret) {
                mpp_frame_deinit(&frame);
                return -4;
            }
            dec->frm_grp = grp;
        } else {
            grp = dec->frm_grp;
        }

        ret = mpp_buffer_group_limit_config(grp, buf_size, 24);
        if (ret) {
            mpp_frame_deinit(&frame);
            return -5;
        }

        ret = dec->mpi->control(dec->ctx, MPP_DEC_SET_EXT_BUF_GROUP, grp);
        if (ret) {
            mpp_frame_deinit(&frame);
            return -6;
        }

        ret = dec->mpi->control(dec->ctx, MPP_DEC_SET_INFO_CHANGE_READY, NULL);
        if (ret) {
            mpp_frame_deinit(&frame);
            return -7;
        }

        dec->width  = (int)width;
        dec->height = (int)height;
        dec->src_buf_size = buf_size;

        /* (Re-)allocate persistent aligned RGB dst buffer for RGA */
        if (dec->dst_handle) {
            releasebuffer_handle(dec->dst_handle);
            dec->dst_handle = 0;
        }
        if (dec->dst_rgb_buf) {
            free(dec->dst_rgb_buf);
            dec->dst_rgb_buf = NULL;
        }

        int wstride  = ((int)width + 3) & ~3;
        int dst_size = wstride * (int)height * 3;

        dec->dst_rgb_buf  = (uint8_t *)malloc(dst_size);
        if (!dec->dst_rgb_buf) {
            mpp_frame_deinit(&frame);
            return -13;
        }
        dec->dst_rgb_size = dst_size;
        dec->dst_wstride  = wstride;

        im_handle_param_t dst_param = {0};
        dst_param.width  = (uint32_t)wstride;
        dst_param.height = (uint32_t)height;
        dst_param.format = RK_FORMAT_RGB_888;
        dec->dst_handle = importbuffer_virtualaddr(dec->dst_rgb_buf, &dst_param);
        if (!dec->dst_handle) {
            free(dec->dst_rgb_buf);
            dec->dst_rgb_buf = NULL;
            mpp_frame_deinit(&frame);
            return -14;
        }

        mpp_frame_deinit(&frame);
        return 1;
    }

    {
        RK_U32 err_info = mpp_frame_get_errinfo(frame);
        RK_U32 discard = mpp_frame_get_discard(frame);
        if (err_info || discard) {
            mpp_frame_deinit(&frame);
            return 1;
        }
    }

    {
        MppBuffer buf = mpp_frame_get_buffer(frame);
        if (!buf) {
            mpp_frame_deinit(&frame);
            return -8;
        }

        MppFrameFormat fmt = mpp_frame_get_fmt(frame);
        if (fmt != MPP_FMT_YUV420SP && fmt != MPP_FMT_YUV420SP_VU) {
            mpp_frame_deinit(&frame);
            return -9;
        }

        int ret_rga = rga_nv12_to_rgb(dec, frame,
                                      rgb_buffer,
                                      dec->width,
                                      dec->height);
        if (ret_rga != 0) {
            mpp_frame_deinit(&frame);
            return ret_rga;
        }
    }

    if (mpp_frame_get_eos(frame))
        dec->eos_reached = 1;

    mpp_frame_deinit(&frame);

    return 0;
}

void close_rkmpp_decoder_native(NativeRkmppDecoder *dec)
{
    if (!dec)
        return;

    for (int i = 0; i < dec->cache_count; i++)
        releasebuffer_handle(dec->cached_handles[i]);
    dec->cache_count = 0;

    if (dec->dst_handle) {
        releasebuffer_handle(dec->dst_handle);
        dec->dst_handle = 0;
    }
    if (dec->dst_rgb_buf) {
        free(dec->dst_rgb_buf);
        dec->dst_rgb_buf = NULL;
    }

    if (dec->ctx) {
        dec->mpi->reset(dec->ctx);
        mpp_destroy(dec->ctx);
        dec->ctx = NULL;
        dec->mpi = NULL;
    }

    if (dec->cfg) {
        mpp_dec_cfg_deinit(dec->cfg);
        dec->cfg = NULL;
    }

    if (dec->frm_grp) {
        mpp_buffer_group_put(dec->frm_grp);
        dec->frm_grp = NULL;
    }
}

