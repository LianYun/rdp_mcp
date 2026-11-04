// 实时画面输入换算（纯函数，便于单测）。

import type { MouseButton } from "@/api/types";

/** MouseEvent.button -> 服务端按钮名。 */
export function mouseButton(b: number): MouseButton | null {
  switch (b) {
    case 0:
      return "left";
    case 1:
      return "middle";
    case 2:
      return "right";
    case 3:
      return "x1";
    case 4:
      return "x2";
    default:
      return null;
  }
}

/**
 * WheelEvent -> 滚轮格数（浏览器方向：dy>0 向下）。
 * 像素模式 100px 为一格，行模式 3 行一格，页模式一页算 10 格；至少 1 格。
 */
export function wheelNotches(delta: number, deltaMode: number): number {
  if (delta === 0) return 0;
  let n: number;
  if (deltaMode === 1) n = delta / 3;
  else if (deltaMode === 2) n = delta * 10;
  else n = delta / 100;
  const r = Math.round(Math.abs(n)) || 1;
  return Math.sign(delta) * Math.min(r, 20);
}

export interface Size {
  w: number;
  h: number;
}

/** 元素内坐标（CSS 像素）-> 帧坐标；元素大小为 0 时返回 null。 */
export function toFrameCoords(offsetX: number, offsetY: number, shown: Size, frame: Size): { x: number; y: number } | null {
  if (shown.w <= 0 || shown.h <= 0 || frame.w <= 0 || frame.h <= 0) return null;
  const x = Math.min(frame.w - 1, Math.max(0, Math.round((offsetX * frame.w) / shown.w)));
  const y = Math.min(frame.h - 1, Math.max(0, Math.round((offsetY * frame.h) / shown.h)));
  return { x, y };
}

export interface CursorGeometry {
  left: number;
  top: number;
  width: number;
  height: number;
}

/**
 * 光标叠加层的位置与大小（CSS 像素）。
 * x/y 为帧坐标；hot、size 为桌面像素；scale 为 帧像素/桌面像素；k 为 显示像素/帧像素。
 */
export function cursorGeometry(
  cur: { x: number; y: number; hot: [number, number]; size: [number, number] },
  scale: number,
  k: number,
): CursorGeometry {
  const s = scale * k;
  return { left: cur.x * k - cur.hot[0] * s, top: cur.y * k - cur.hot[1] * s, width: cur.size[0] * s, height: cur.size[1] * s };
}

/** 帧在容器内按比例缩放（不放大超过 1:1 之外的限制由调用者决定），返回显示尺寸。 */
export function fitSize(frame: Size, box: Size): Size {
  if (frame.w <= 0 || frame.h <= 0 || box.w <= 0 || box.h <= 0) return { w: 0, h: 0 };
  const k = Math.min(box.w / frame.w, box.h / frame.h);
  return { w: Math.floor(frame.w * k), h: Math.floor(frame.h * k) };
}

/** 滑动窗口帧率计数。 */
export class FpsMeter {
  private stamps: number[] = [];
  constructor(private windowMs = 2000) {}
  tick(now: number): void {
    this.stamps.push(now);
    this.trim(now);
  }
  value(now: number): number {
    this.trim(now);
    if (this.stamps.length < 2) return this.stamps.length;
    const span = (this.stamps[this.stamps.length - 1] - this.stamps[0]) / 1000;
    return span > 0 ? Math.round(((this.stamps.length - 1) / span) * 10) / 10 : 0;
  }
  private trim(now: number) {
    while (this.stamps.length && now - this.stamps[0] > this.windowMs) this.stamps.shift();
  }
}
