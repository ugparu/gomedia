# merge-mp4s

Concatenate every `.mp4` file in a directory into a single output MP4. Packet timestamps from subsequent files are shifted so the merged stream is monotonic.

## Run

```bash
SRC_DIR=./src go run .
```

If `SRC_DIR` is unset, the program reads from `./src`. Output is written to `./merged.mp4`.
