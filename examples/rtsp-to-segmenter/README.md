# RTSP to Segmenter - Multi-URL Example

This example demonstrates the multi-URL segmenter functionality, showing how to record multiple RTSP streams simultaneously to MP4 files with automatic segmentation.

## Features

- **Multi-URL Support**: Record from multiple RTSP streams concurrently
- **Index-Based Naming**: Files are automatically named with stream index prefix
- **Automatic Segmentation**: Files are rotated based on duration
- **Organized Storage**: Files stored in date-based folder structure

## File Naming Convention

The segmenter creates files with the following naming pattern:

```
./recordings/YYYY/MM/DD/<stream_index>_<timestamp>.mp4
```

### Examples:

- **Stream 0** (first URL): `recordings/2026/01/13/0_2026-01-13T15:04:05.mp4`
- **Stream 1** (second URL): `recordings/2026/01/13/1_2026-01-13T15:04:05.mp4`
- **Stream 2** (third URL): `recordings/2026/01/13/2_2026-01-13T15:04:05.mp4`

The stream index corresponds to the order in which URLs were added via `AddSource()`.

## Usage

### Single Stream

```bash
RTSP_URLS="rtsp://username:password@camera1.local:554/stream" go run main.go
```

### Multiple Streams

```bash
RTSP_URLS="rtsp://camera1:554/stream,rtsp://camera2:554/stream,rtsp://camera3:554/stream" go run main.go
```

## Configuration

Edit `main.go` to customize:

- **Segment Duration**: Change `segmentDuration` constant (default: 15 seconds)
- **Output Directory**: Modify the first parameter in `segmenter.New()` (default: `./recordings/`)
- **Record Mode**: Change the third parameter:
  - `gomedia.Always`: Continuous recording
  - `gomedia.Event`: Record only when events are triggered
  - `gomedia.Never`: No recording

## How It Works

1. **Source Registration**: Each RTSP URL is registered with the segmenter via `AddSource()`
2. **Stream State**: The segmenter maintains separate state for each URL (active file, ring buffer, codec parameters)
3. **Packet Routing**: Incoming packets are routed to the correct stream based on their URL
4. **File Creation**: Each stream creates files independently with its index in the filename
5. **Shared Control**: Recording mode and events affect all streams simultaneously

## Example Output

```
INFO Starting multi-URL RTSP to Segmenter example...
INFO Connecting to 2 RTSP stream(s): [rtsp://camera1:554/stream rtsp://camera2:554/stream]
INFO Segmenter initialized: destination=./recordings/, segment_duration=15s, record_mode=Always
INFO Adding source 0: rtsp://camera1:554/stream
INFO Adding source 1: rtsp://camera2:554/stream
INFO ✅ All systems running. Press Ctrl+C to stop.
INFO 📁 Recording files to: ./recordings/YYYY/MM/DD/
INFO 📝 File naming format: <stream_index>_<timestamp>.mp4
INFO 📹 Recording started
INFO 📊 Processed 500 total packets:
INFO    Stream 0 (rtsp://camera1:554/stream): 250 packets
INFO    Stream 1 (rtsp://camera2:554/stream): 250 packets
INFO ✓ Recorded segment: 2026/1/13/0_2026-01-13T15:04:05.mp4 (size: 1048576 bytes, duration: 15s)
INFO ✓ Recorded segment: 2026/1/13/1_2026-01-13T15:04:05.mp4 (size: 987654 bytes, duration: 15s)
```

## Testing Without Real Cameras

If you don't have real RTSP cameras, you can use FFmpeg to create test streams:

```bash
# Terminal 1: Create test stream 1
ffmpeg -re -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine=frequency=1000 \
  -c:v libx264 -preset ultrafast -tune zerolatency -c:a aac \
  -f rtsp rtsp://localhost:8554/stream1

# Terminal 2: Create test stream 2
ffmpeg -re -f lavfi -i testsrc=size=640x480:rate=30 -f lavfi -i sine=frequency=500 \
  -c:v libx264 -preset ultrafast -tune zerolatency -c:a aac \
  -f rtsp rtsp://localhost:8554/stream2

# Terminal 3: Run the example
RTSP_URLS="rtsp://localhost:8554/stream1,rtsp://localhost:8554/stream2" go run main.go
```

## Notes

- Each stream can have different codec parameters
- Files are closed and new ones opened when segment duration is reached (on keyframe)
- The segmenter automatically handles codec parameter changes
- Removing a source (via `RemoveSource()`) shifts the indices of subsequent sources

