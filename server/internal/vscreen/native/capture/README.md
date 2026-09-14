# Capture and encoder ownership

The parent package authorizes the source. This package receives one exact display ID and never picks a fallback. `Open` validates bounds and cancellation before native access, then checks screen-recording consent without prompting. A denied permission returns `ErrPermission` before content enumeration or stream creation.

The native capture callback retains only the newest pending CVPixelBuffer. A serial encoder queue submits at most two in-flight frames. VideoToolbox callbacks copy Annex-B access units into a three-sample queue; overflow invalidates deltas and forces an IDR. No native buffer pointer reaches a consumer after its owning callback. Stream.Next returns owned Go bytes.

Close first freezes delivery, stops SCStream, removes the output, drains the delivery queue, then completes and invalidates the encoder. A timeout/error preserves the handle and must keep the source unavailable; callers must not infer quiescence from cancellation alone. Open can return a nonnil stream alongside a cancellation-cleanup error, which the caller must retain for cleanup. Stalled native captures are admission-bounded.

`MaxLevelIDC` is optional; currently supported negotiated caps are 31 and 40. At 1280x720/30 the measured ConstrainedBaseline auto encoder produced `42c01f`; at 1600x900/30 it produced `42c028`. Actual SPS is verified before forwarding, and Stats exposes the measured codec and whether the hardware-selection query was available. This does not establish a browser's negotiated receive capability.

Native integration tests are opt-in (`-tags=nativeintegration`) and require `MULTICA_VSCREEN_SMOKE_EVIDENCE`. Synthetic encoder fixtures create task-owned BGRA buffers only; they never capture a screen. The denial fixture first verifies consent is absent, then checks refusal. It refuses to run its capture call if consent is present. None of these fixtures establishes real SCStream frame delivery or screen-recording performance.
