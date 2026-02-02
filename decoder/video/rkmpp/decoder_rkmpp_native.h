/* SPDX-License-Identifier: Apache-2.0 OR MIT */
/*
 * Simple RKMPP-based decoder wrapper for Go.
 *
 * This wrapper uses the native Rockchip MPP API (rk_mpi.h) directly,
 * without any FFmpeg dependency. It provides a minimal C interface that
 * is convenient to call from cgo.
 */

#ifndef DECODER_RKMPP_NATIVE_H
#define DECODER_RKMPP_NATIVE_H

#include <stdint.h>

#include "rk_mpi.h"

#ifndef MAX_WIDTH
#define MAX_WIDTH 4096
#endif

#ifndef MAX_HEIGHT
#define MAX_HEIGHT 3072
#endif

typedef struct {
    MppCtx      ctx;
    MppApi     *mpi;

    MppDecCfg   cfg;

    MppBufferGroup frm_grp;

    MppFrame    frame;

    RK_S32      width;
    RK_S32      height;

    MppCodingType coding_type;

    RK_U32      eos_reached;
} NativeRkmppDecoder;

/*
 * Initialize RKMPP decoder.
 *
 * codec_id:
 *   1 - H.264 (AVC)
 *   2 - H.265 (HEVC)
 *
 * width / height:
 *   expected coded dimensions of the stream.
 *
 * Returns 0 on success, negative on error.
 */
int init_rkmpp_decoder_native(NativeRkmppDecoder *dec,
                              int codec_id,
                              int width,
                              int height);

/*
 * Feed one Annex-B encoded video packet into the decoder.
 *
 * IMPORTANT: With split_parse=0, each call must contain exactly ONE NAL unit.
 * The packet MUST start with Annex-B start code (0x00000001).
 * The packet must contain only one frame/NAL unit.
 *
 * data / size:
 *   single Annex-B NAL unit with 0x00000001 start code at the beginning.
 *
 * pts_ms:
 *   presentation timestamp in milliseconds.
 *
 * Returns 0 on success, >0 when packet is ignored but not fatal,
 * negative value on hard error.
 */
int feed_rkmpp_packet_native(NativeRkmppDecoder *dec,
                             const uint8_t *data,
                             int size,
                             int64_t pts_ms);

/*
 * Try to obtain one decoded frame and convert it to RGB24.
 *
 * rgb_buffer:
 *   output buffer in RGB24 format, size must be at least width * height * 3.
 *   Width/height must match values passed on initialization.
 *
 * rgb_buf_size:
 *   size of rgb_buffer in bytes.
 *
 * Return codes:
 *   0  - frame successfully decoded and written to rgb_buffer
 *   >0 - no frame available yet (need more data)
 *   <0 - error
 */
int decode_rkmpp_frame_native(NativeRkmppDecoder *dec,
                              uint8_t *rgb_buffer,
                              int rgb_buf_size);

/*
 * Convert one decoded NV12 frame from MPP to RGB24 using Rockchip RGA.
 *
 * frame:
 *   MppFrame with pixel format NV12/NV21 (YUV420 semi-planar) backed by
 *   a DMA buffer (fd accessible via mpp_buffer_get_fd()).
 *
 * dst_buffer:
 *   Destination buffer in RGB24 format, size must be at least
 *   dst_width * dst_height * 3 bytes.
 *
 * Returns 0 on success, negative value on error.
 */
int rga_nv12_to_rgb(MppFrame frame,
                    uint8_t *dst_buffer,
                    int dst_width,
                    int dst_height);

/*
 * Release all resources associated with the decoder.
 */
void close_rkmpp_decoder_native(NativeRkmppDecoder *dec);

#endif /* DECODER_RKMPP_NATIVE_H */

