# gomedia examples

End-to-end pipelines built on the top-level `gomedia` interfaces. Each subdirectory is a self-contained `main` package; run with `go run .` from that directory.

| Example | What it does |
|---|---|
| [aac-to-alaw](aac-to-alaw/)                 | Decode AAC from RTSP, re-encode as PCM A-law, write to a raw `.pcm` file. |
| [log-rtsp-packets](log-rtsp-packets/)       | Connect to RTSP and consume packets in a tight loop — useful for stress testing. |
| [logger](logger/)                           | Shared logrus-based `logger.Logger` implementation used by other examples. Not runnable. |
| [merge-mp4s](merge-mp4s/)                   | Concatenate every `.mp4` in a directory into a single MP4 with rewritten timestamps. |
| [mjpeg-rtsp-to-jpeg](mjpeg-rtsp-to-jpeg/)   | Save MJPEG frames from an RTSP source as individual `.jpg` files. |
| [mp4-to-jpeg](mp4-to-jpeg/)                 | Decode the first frame of an MP4 (H.264/H.265) to JPEG via the CPU/FFmpeg decoder. |
| [mp4-to-mp4](mp4-to-mp4/)                   | Remux an MP4 file (demux → mux, no transcoding). |
| [mp4-to-rtsp](mp4-to-rtsp/)                 | Demux an MP4 and publish it over RTSP. Note: the RTP muxer is currently a stub. |
| [rkmpp-rtsp-info](rkmpp-rtsp-info/)         | Parallel RTSP ingest + Rockchip MPP hardware decode with stats. Requires the `rkmpp` build tag. |
| [rkmpp-rtsp-to-jpeg](rkmpp-rtsp-to-jpeg/)   | RTSP → RKMPP-decoded JPEG. Requires the `rkmpp` build tag. |
| [rtsp-to-all](rtsp-to-all/)                 | Fan one or more RTSP sources to HLS + WebRTC + MP4 segmenter behind a Gin HTTP server. |
| [rtsp-to-hls](rtsp-to-hls/)                 | RTSP → Low-Latency HLS (LL-HLS), served over HTTP. |
| [rtsp-to-jpeg](rtsp-to-jpeg/)               | RTSP → CUDA-decoded JPEG. Requires the `cuda` build tag and NVIDIA Video Codec SDK. |
| [rtsp-to-json](rtsp-to-json/)               | Capture N packets from RTSP and dump codec params + packet data to JSON (test fixtures). |
| [rtsp-to-mp4](rtsp-to-mp4/)                 | Minimal end-to-end pipeline — record the first 100 RTSP packets to an MP4 file. |
| [rtsp-to-rtsp](rtsp-to-rtsp/)               | Relay one RTSP stream to another RTSP server. |
| [rtsp-to-segmenter](rtsp-to-segmenter/)     | RTSP → archive segmenter producing rotating MP4 files (multi-source). |
| [rtsp-to-webrtc](rtsp-to-webrtc/)           | RTSP → WebRTC with a Gin HTTP server handling SDP offer/answer and dynamic URL list. |
| [rtsp-transcode-audio-to-mp4](rtsp-transcode-audio-to-mp4/) | RTSP → transcode audio (PCM/AAC) → MP4 with audio + video. |

Most examples take `RTSP_URL` (or `RTSP_URLS` for fan-out) from the environment. CGo-gated examples require the corresponding build tag — see each example's README.
