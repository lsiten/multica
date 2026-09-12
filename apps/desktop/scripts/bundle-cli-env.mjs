// macOS screen capture links CoreGraphics through a CGO package. Windows and
// Linux stay CGO-free so Desktop can still cross-package portable CLIs.
export function cgoEnabledForGoos(goos) {
  return goos === "darwin" ? "1" : "0";
}
