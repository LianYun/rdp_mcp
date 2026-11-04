// 单测环境：jsdom 里没有 EventSource/matchMedia，补最小实现。
import { afterEach } from "vitest";

if (!("matchMedia" in window)) {
  Object.defineProperty(window, "matchMedia", {
    value: (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }),
  });
}

afterEach(() => {
  localStorage.clear();
});
