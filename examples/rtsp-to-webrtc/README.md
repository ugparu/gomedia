# rtsp-to-webrtc

RTSP → WebRTC streaming with a small Gin HTTP server on port `8080`. The dynamic URL list lets you add or remove sources at runtime via `POST /url`; clients negotiate via `POST /sdp`.

## Run

```bash
go run .
```

Then open `http://localhost:8080/`. The bundled `index.html` provides the offer/answer flow and a video element.

The WebRTC binder is initialized with a fixed ICE port range (`2000-2100`) and a single hardcoded public host — edit the `webrtc.Init(...)` call to match your network.

Native deps: `libopus-dev`, `libopusfile-dev`.
