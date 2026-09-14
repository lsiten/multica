These H.264 Annex-B fixtures contain synthetic pixel-buffer patterns generated
by the repository's VideoToolbox tests, without screen capture or TCC access.

- `video-1600x900.h264`: `TestVideoToolboxSyntheticEncode`, six frames,
  constrained baseline, SPS `42c028`, no B frames, forced IDR at frame 3.
- `video-1280x720.h264`: `TestVideoToolboxNegotiated720p`, six frames,
  constrained baseline, SPS `42c01f`, no B frames, forced IDR at frame 3.

The transport tests extract SPS/PPS plus the first IDR, send it through real
Pion PeerConnections, and compare the depacketized access unit. The opt-in
`browserintegration` test serves a local fixture page for a separate Chromium
receiver and records actual `videoWidth`, `videoHeight`, `framesDecoded`, and
control metadata. Neither fixture demonstrates physical display capture.
