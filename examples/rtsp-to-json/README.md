# rtsp-to-json

Capture N packets from an RTSP source and dump everything needed for offline testing — codec parameters and packet payloads — into JSON files. Used to produce fixtures under `tests/data/`.

## Run

```bash
go run . -url rtsp://camera/stream -n 200 -params-file parameters.json -packets-file packets.json
```

Flags:

- `-url`           RTSP URL (required).
- `-n`             Number of packets to capture (default `100`).
- `-params-file`   Output path for codec parameters.
- `-packets-file`  Output path for the packet stream.
- `-split`         Write one parameters/packets file per codec, e.g. `packets_H264.json`.

Packet payloads are base64-encoded. Codec parameters include SPS/PPS/VPS for H.264/H.265 and the AudioSpecificConfig for AAC when the underlying codec parameter type exposes them.
