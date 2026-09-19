export interface ViewportPoint {
  readonly clientX: number;
  readonly clientY: number;
}

export interface ElementRect {
  readonly left: number;
  readonly top: number;
  readonly width: number;
  readonly height: number;
}

export interface SourceFrame {
  readonly width: number;
  readonly height: number;
}

export interface SourcePoint {
  readonly x: number;
  readonly y: number;
}

function finitePositive(value: number): boolean {
  return Number.isFinite(value) && value > 0;
}

/**
 * Map a viewport point to source-frame pixels. The video uses object-fit:
 * contain, so points in letterbox areas clamp to the visible source edge.
 * Client coordinates and getBoundingClientRect() use the same zoomed visual
 * viewport coordinate system; no additional visualViewport or devicePixelRatio
 * scale is needed.
 */
export function pointToSourceFrame(
  point: ViewportPoint,
  rect: ElementRect,
  source: SourceFrame,
): SourcePoint | null {
  if (
    !Number.isFinite(point.clientX) ||
    !Number.isFinite(point.clientY) ||
    !finitePositive(rect.width) ||
    !finitePositive(rect.height) ||
    !finitePositive(source.width) ||
    !finitePositive(source.height) ||
    !Number.isFinite(rect.left) ||
    !Number.isFinite(rect.top)
  ) {
    return null;
  }

  const scale = Math.min(rect.width / source.width, rect.height / source.height);
  const contentWidth = source.width * scale;
  const contentHeight = source.height * scale;
  const contentLeft = rect.left + (rect.width - contentWidth) / 2;
  const contentTop = rect.top + (rect.height - contentHeight) / 2;

  const x = ((point.clientX - contentLeft) / contentWidth) * source.width;
  const y = ((point.clientY - contentTop) / contentHeight) * source.height;

  return {
    x: Math.min(source.width, Math.max(0, x)),
    y: Math.min(source.height, Math.max(0, y)),
  };
}
