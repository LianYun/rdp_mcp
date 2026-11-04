import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import type { ApiClient } from "@/api/client";
import { LiveStream } from "@/api/stream";
import type { ControlMode, MachineInfo, MouseButton, StreamInput, StreamMessage } from "@/api/types";
import { useT } from "@/i18n";
import { cursorGeometry, fitSize, FpsMeter, mouseButton, toFrameCoords, wheelNotches, type Size } from "@/lib/input";
import { cn } from "@/lib/utils";

export interface LiveViewHandle {
  sendCtrlAltDel: () => void;
  sendText: (text: string) => void;
  sendClipboard: (text: string) => void;
}

interface Props {
  client: ApiClient;
  machine: MachineInfo;
  /** 服务端推送的错误（如“接管后才能输入”）。 */
  onServerError?: (msg: string) => void;
}

interface Cursor {
  x: number;
  y: number;
  visible: boolean;
}

const DEFAULT_ARROW_SIZE: [number, number] = [12, 19];

export const LiveView = forwardRef<LiveViewHandle, Props>(function LiveView({ client, machine, onServerError }, ref) {
  const t = useT();
  const takeover = machine.control_mode === "human";
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const streamRef = useRef<LiveStream | null>(null);

  const [frame, setFrame] = useState<Size>({ w: 0, h: 0 });
  const [scale, setScale] = useState(1);
  const [box, setBox] = useState<Size>({ w: 0, h: 0 });
  const [cursor, setCursor] = useState<Cursor>({ x: 0, y: 0, visible: false });
  const [shape, setShape] = useState<{ url: string; hot: [number, number]; size: [number, number] }>({
    url: "",
    hot: [0, 0],
    size: DEFAULT_ARROW_SIZE,
  });
  const [open, setOpen] = useState(false);
  const [gotFrame, setGotFrame] = useState(false);
  const [fps, setFps] = useState(0);
  const [latency, setLatency] = useState<number | null>(null);
  const [focused, setFocused] = useState(false);

  const fpsMeter = useRef(new FpsMeter());
  const frameCount = useRef(0);
  const frameSeq = useRef(0);
  const lastInputAt = useRef(0);
  const frameSize = useRef<Size>({ w: 0, h: 0 });
  const shown = useMemo(() => fitSize(frame, box), [frame, box]);
  const shownRef = useRef<Size>({ w: 0, h: 0 });
  shownRef.current = shown;
  const takeoverRef = useRef(takeover);
  takeoverRef.current = takeover;
  const pressedKeys = useRef(new Set<string>());
  const pressedButtons = useRef(new Set<string>());
  const lastPos = useRef({ x: 0, y: 0 });
  const pendingMove = useRef<{ x: number; y: number } | null>(null);
  const moveRaf = useRef(0);
  const onServerErrorRef = useRef(onServerError);
  onServerErrorRef.current = onServerError;

  const send = useCallback((m: StreamInput) => {
    lastInputAt.current = performance.now();
    streamRef.current?.send(m);
  }, []);

  useImperativeHandle(
    ref,
    () => ({
      sendCtrlAltDel: () => {
        const seq: [string, boolean][] = [
          ["ControlLeft", true],
          ["AltLeft", true],
          ["Delete", true],
          ["Delete", false],
          ["AltLeft", false],
          ["ControlLeft", false],
        ];
        for (const [code, down] of seq) send({ type: "key", code, down });
      },
      sendText: (text) => send({ type: "text", text }),
      sendClipboard: (text) => send({ type: "clipboard", text }),
    }),
    [send],
  );

  // 容器尺寸
  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setBox({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    setBox({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, []);

  // 画面帧
  const drawFrame = useCallback(async (blob: Blob) => {
    const seq = ++frameSeq.current;
    let bmp: ImageBitmap;
    try {
      bmp = await createImageBitmap(blob);
    } catch {
      return;
    }
    if (seq !== frameSeq.current) {
      bmp.close(); // 已有更新的帧
      return;
    }
    const cv = canvasRef.current;
    if (!cv) {
      bmp.close();
      return;
    }
    if (cv.width !== bmp.width || cv.height !== bmp.height) {
      cv.width = bmp.width;
      cv.height = bmp.height;
    }
    cv.getContext("2d")?.drawImage(bmp, 0, 0);
    if (frameSize.current.w !== bmp.width || frameSize.current.h !== bmp.height) {
      frameSize.current = { w: bmp.width, h: bmp.height };
      setFrame({ w: bmp.width, h: bmp.height });
    }
    bmp.close();
    const now = performance.now();
    fpsMeter.current.tick(now);
    frameCount.current++;
    cv.dataset.frames = String(frameCount.current);
    setGotFrame(true);
    if (lastInputAt.current) {
      setLatency(Math.round(now - lastInputAt.current));
      lastInputAt.current = 0;
    }
  }, []);

  // WebSocket（连接/断开由 REST 控制，这里不触发服务端自动连接）
  useEffect(() => {
    let closed = false;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    const start = () => {
      const s = new LiveStream(
        client,
        machine.name,
        { fps: 15, maxWidth: 1920, quality: 70, connect: false },
        {
          onFrame: (b) => void drawFrame(b),
          onMessage: (m: StreamMessage) => {
            switch (m.type) {
              case "meta":
                setScale(m.scale || 1);
                break;
              case "cursor":
                setCursor({ x: m.x, y: m.y, visible: m.visible });
                if (m.shape !== undefined) {
                  setShape({
                    url: m.shape ? `data:image/png;base64,${m.shape}` : "",
                    hot: m.hot ?? [0, 0],
                    size: m.size ?? DEFAULT_ARROW_SIZE,
                  });
                }
                break;
              case "error":
                onServerErrorRef.current?.(m.message);
                break;
            }
          },
          onOpen: () => {
            attempt = 0;
            setOpen(true);
          },
          onClose: () => {
            setOpen(false);
            if (!closed) {
              attempt++;
              retry = setTimeout(start, Math.min(5000, 800 * attempt));
            }
          },
        },
      );
      streamRef.current = s;
      s.open();
    };
    start();
    return () => {
      closed = true;
      if (retry) clearTimeout(retry);
      streamRef.current?.close();
      streamRef.current = null;
    };
  }, [client, machine.name, drawFrame]);

  // 断开连接后清除画面状态
  useEffect(() => {
    if (machine.state !== "connected") {
      setGotFrame(false);
      setCursor((c) => ({ ...c, visible: false }));
    }
  }, [machine.state]);

  // 帧率读数
  useEffect(() => {
    const id = setInterval(() => setFps(fpsMeter.current.value(performance.now())), 500);
    return () => clearInterval(id);
  }, []);

  const releaseAll = useCallback(() => {
    for (const code of pressedKeys.current) streamRef.current?.send({ type: "key", code, down: false });
    pressedKeys.current.clear();
    const cur = lastPos.current;
    for (const b of pressedButtons.current) {
      streamRef.current?.send({ type: "mouse", action: "up", x: cur.x, y: cur.y, button: b as MouseButton });
    }
    pressedButtons.current.clear();
  }, []);

  // 离开接管模式 / 失去焦点 / 卸载时松开所有按键
  useEffect(() => {
    if (!takeover) releaseAll();
  }, [takeover, releaseAll]);
  useEffect(() => releaseAll, [releaseAll]);

  const coords = (e: { offsetX: number; offsetY: number }) => toFrameCoords(e.offsetX, e.offsetY, shownRef.current, frameSize.current);

  const flushMove = useCallback(() => {
    moveRaf.current = 0;
    const p = pendingMove.current;
    pendingMove.current = null;
    if (p) send({ type: "mouse", action: "move", x: p.x, y: p.y });
  }, [send]);

  const onMouseMove = (e: React.MouseEvent) => {
    if (!takeover) return;
    const c = coords(e.nativeEvent);
    if (!c) return;
    lastPos.current = c;
    pendingMove.current = c;
    if (!moveRaf.current) moveRaf.current = requestAnimationFrame(flushMove);
  };

  const onMouseDown = (e: React.MouseEvent) => {
    inputRef.current?.focus();
    if (!takeover) return;
    const b = mouseButton(e.button);
    const c = coords(e.nativeEvent);
    if (!b || !c) return;
    e.preventDefault();
    lastPos.current = c;
    pressedButtons.current.add(b);
    send({ type: "mouse", action: "down", x: c.x, y: c.y, button: b });
  };

  // 在窗口上监听 mouseup，拖出画面后释放也能送达
  useEffect(() => {
    const up = (e: MouseEvent) => {
      if (!takeoverRef.current) return;
      const b = mouseButton(e.button);
      if (!b || !pressedButtons.current.has(b)) return;
      pressedButtons.current.delete(b);
      const cv = canvasRef.current;
      let pos = lastPos.current;
      if (cv) {
        const r = cv.getBoundingClientRect();
        const c = toFrameCoords(e.clientX - r.left, e.clientY - r.top, shownRef.current, frameSize.current);
        if (c) pos = c;
      }
      send({ type: "mouse", action: "up", x: pos.x, y: pos.y, button: b });
    };
    window.addEventListener("mouseup", up);
    return () => window.removeEventListener("mouseup", up);
  }, [send]);

  // 滚轮需要 passive:false 才能 preventDefault
  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv) return;
    const h = (e: WheelEvent) => {
      if (!takeoverRef.current) return;
      e.preventDefault();
      const c = toFrameCoords(e.offsetX, e.offsetY, shownRef.current, frameSize.current);
      if (!c) return;
      const dy = wheelNotches(e.deltaY, e.deltaMode);
      const dx = wheelNotches(e.deltaX, e.deltaMode);
      if (dy || dx) send({ type: "mouse", action: "wheel", x: c.x, y: c.y, ...(dy ? { dy } : {}), ...(dx ? { dx } : {}) });
    };
    cv.addEventListener("wheel", h, { passive: false });
    return () => cv.removeEventListener("wheel", h);
  }, [send]);

  const composing = (e: React.KeyboardEvent) => e.nativeEvent.isComposing || e.keyCode === 229;

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!takeover || composing(e)) return;
    e.preventDefault();
    pressedKeys.current.add(e.code);
    send({ type: "key", code: e.code, down: true });
  };
  const onKeyUp = (e: React.KeyboardEvent) => {
    if (!takeover || composing(e)) return;
    e.preventDefault();
    pressedKeys.current.delete(e.code);
    send({ type: "key", code: e.code, down: false });
  };
  const onCompositionEnd = (e: React.CompositionEvent<HTMLInputElement>) => {
    if (takeover && e.data) send({ type: "text", text: e.data });
    if (inputRef.current) inputRef.current.value = "";
  };

  const mode: ControlMode = machine.control_mode;
  const geo = cursorGeometry(
    { x: cursor.x, y: cursor.y, hot: shape.hot, size: shape.size },
    scale,
    frame.w > 0 ? shown.w / frame.w : 1,
  );
  const connected = machine.state === "connected";

  return (
    <div
      className={cn(
        "relative flex h-full min-h-[240px] flex-col overflow-hidden rounded-lg border-2 bg-black transition-colors",
        takeover ? "border-warning" : "border-transparent",
        takeover && focused && "ring-2 ring-warning/50",
      )}
      data-testid="live-view"
      data-takeover={takeover}
      data-mode={mode}
    >
      <div ref={boxRef} className="relative flex min-h-0 flex-1 items-center justify-center">
        <div className="relative" style={{ width: shown.w || undefined, height: shown.h || undefined }}>
          <canvas
            ref={canvasRef}
            data-testid="live-canvas"
            className={cn("block", takeover && "cursor-none")}
            style={{ width: shown.w || 0, height: shown.h || 0, display: gotFrame ? "block" : "none" }}
            onMouseMove={onMouseMove}
            onMouseDown={onMouseDown}
            onContextMenu={(e) => e.preventDefault()}
          />
          {gotFrame && cursor.visible && connected && (
            <div
              data-testid="live-cursor"
              className="pointer-events-none absolute"
              style={{ left: geo.left, top: geo.top, width: geo.width, height: geo.height }}
            >
              {shape.url ? (
                <img src={shape.url} alt="" className="size-full" draggable={false} style={{ imageRendering: "auto" }} />
              ) : (
                <svg viewBox="0 0 12 19" className="size-full" aria-hidden>
                  <path d="M0 0v16l4.2-3.8L7 19l2.4-1.1-2.7-6.2H12z" fill="#fff" stroke="#000" strokeWidth="1" />
                </svg>
              )}
            </div>
          )}
        </div>
        {!gotFrame && (
          <div className="absolute inset-0 flex items-center justify-center text-sm text-white/70" data-testid="live-placeholder">
            {connected ? t("detail.waiting_frame") : t("detail.not_connected")}
          </div>
        )}
        {/* 隐藏输入框：接管时接收键盘与输入法组合输入 */}
        <input
          ref={inputRef}
          data-testid="live-input"
          aria-label="remote keyboard input"
          className="absolute left-0 top-0 size-px opacity-0"
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          onKeyDown={onKeyDown}
          onKeyUp={onKeyUp}
          onCompositionEnd={onCompositionEnd}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            setFocused(false);
            releaseAll();
          }}
        />
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-white/10 bg-neutral-900 px-3 py-1 text-xs text-white/70">
        <span data-testid="live-hint">{takeover ? t("detail.takeover_hint") : t("detail.view_only")}</span>
        <span className="mono flex gap-3" data-testid="live-stats">
          <span data-testid="live-fps">
            {t("detail.fps")} {fps.toFixed(1)}
          </span>
          <span data-testid="live-latency">
            {t("detail.latency")} {latency === null ? "-" : `${latency} ms`}
          </span>
          {frame.w > 0 && (
            <span>
              {frame.w}×{frame.h}
            </span>
          )}
          <span className={open ? "text-green-400" : "text-amber-400"}>{open ? "WS" : t("detail.stream_closed")}</span>
        </span>
      </div>
    </div>
  );
});
