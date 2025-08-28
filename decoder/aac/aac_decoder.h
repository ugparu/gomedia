#include "aacdecoder_lib.h"
#include <stdlib.h>

typedef struct {
	HANDLE_AACDECODER dec;
	// Whether use ADTS mode.
	int is_adts;
	// Init util the first frame decoded.
	CStreamInfo* info;
	// The bits of sample, always 16 for fdkaac.
	int sample_bits;
	// Total filled bytes.
	UINT filled_bytes;
} aacdec_t;

static void _aacdec_init(aacdec_t* h) {
	// For lib-fdkaac, always use 16bits sample.
	// avctx->sample_fmt = AV_SAMPLE_FMT_S16;
	h->sample_bits = 16;
	h->is_adts = 0;
	h->filled_bytes = 0;

	h->dec = NULL;
	h->info = NULL;
}

static int aacdec_init_adts(aacdec_t* h) {
	_aacdec_init(h);

	h->is_adts = 1;

	h->dec = aacDecoder_Open(TT_MP4_ADTS, 1);
	if (!h->dec) {
		return -1;
	}

	return 0;
}

static int aacdec_init_raw(aacdec_t* h, char* asc, int nb_asc) {
	_aacdec_init(h);

	h->dec = aacDecoder_Open(TT_MP4_RAW, 1);
	if (!h->dec) {
		return -1;
	}

	UCHAR* uasc = (UCHAR*)asc;
	UINT unb_asc = (UINT)nb_asc;
	AAC_DECODER_ERROR err = aacDecoder_ConfigRaw(h->dec, &uasc, &unb_asc);
	if (err != AAC_DEC_OK) {
		return err;
	}

	return 0;
}

static void aacdec_close(aacdec_t* h) {
	if (h->dec) {
		aacDecoder_Close(h->dec);
	}
	h->dec = NULL;
}

static int aacdec_fill(aacdec_t* h, char* data, int nb_data, int* pnb_left) {
	h->filled_bytes += nb_data;

	UCHAR* udata = (UCHAR*)data;
	UINT unb_data = (UINT)nb_data;
	UINT unb_left = unb_data;
	AAC_DECODER_ERROR err = aacDecoder_Fill(h->dec, &udata, &unb_data, &unb_left);
	if (err != AAC_DEC_OK) {
		return err;
	}

	if (pnb_left) {
		*pnb_left = (int)unb_left;
	}

	return 0;
}

static int aacdec_sample_bits(aacdec_t* h) {
	return h->sample_bits;
}

static int aacdec_pcm_size(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return (int)(h->info->numChannels * h->info->frameSize * h->sample_bits / 8);
}

static int aacdec_decode_frame(aacdec_t* h, char* pcm, int nb_pcm, int* pnb_valid) {
	// when buffer left bytes not enough, directly return not-enough-bits.
	// we requires atleast 7bytes header for adts.
	if (h->is_adts && h->info && h->filled_bytes - h->info->numTotalBytes <= 7) {
		return AAC_DEC_NOT_ENOUGH_BITS;
	}

	INT_PCM* upcm = (INT_PCM*)pcm;
	INT unb_pcm = (INT)nb_pcm;
	AAC_DECODER_ERROR err = aacDecoder_DecodeFrame(h->dec, upcm, unb_pcm, 0);

	// user should fill more bytes then decode.
	if (err == AAC_DEC_NOT_ENOUGH_BITS) {
		return err;
	}
	if (err != AAC_DEC_OK) {
		return err;
	}

	// when decode ok, retrieve the info.
	if (!h->info) {
		h->info = aacDecoder_GetStreamInfo(h->dec);
	}

	// the actual size of pcm.
	if (pnb_valid) {
		*pnb_valid = aacdec_pcm_size(h);
	}

	return 0;
}

static int aacdec_sample_rate(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->sampleRate;
}

static int aacdec_frame_size(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->frameSize;
}

static int aacdec_num_channels(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->numChannels;
}

static int aacdec_aac_sample_rate(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->aacSampleRate;
}

static int aacdec_profile(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->profile;
}

static int aacdec_audio_object_type(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->aot;
}

static int aacdec_channel_config(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->channelConfig;
}

static int aacdec_bitrate(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->bitRate;
}

static int aacdec_aac_samples_per_frame(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->aacSamplesPerFrame;
}

static int aacdec_aac_num_channels(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->aacNumChannels;
}

static int aacdec_extension_audio_object_type(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->extAot;
}

static int aacdec_extension_sampling_rate(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->extSamplingRate;
}

static int aacdec_num_lost_access_units(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->numLostAccessUnits;
}

static int aacdec_num_total_bytes(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->numTotalBytes;
}

static int aacdec_num_bad_bytes(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->numBadBytes;
}

static int aacdec_num_total_access_units(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->numTotalAccessUnits;
}

static int aacdec_num_bad_access_units(aacdec_t* h) {
	if (!h->info) {
		return 0;
	}
	return h->info->numBadAccessUnits;
}