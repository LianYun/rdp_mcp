import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError, normalizeListen } from "@/api/client";
import { LiveStream } from "@/api/stream";

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("normalizeListen", () => {
  it("maps wildcard addresses to loopback", () => {
    expect(normalizeListen("0.0.0.0:8765")).toBe("127.0.0.1:8765");
    expect(normalizeListen("[::]:8765")).toBe("127.0.0.1:8765");
    expect(normalizeListen(":8765")).toBe("127.0.0.1:8765");
  });
  it("keeps concrete addresses", () => {
    expect(normalizeListen("127.0.0.1:18766")).toBe("127.0.0.1:18766");
    expect(normalizeListen("10.0.0.2:9000")).toBe("10.0.0.2:9000");
  });
});

describe("ApiClient", () => {
  const c = new ApiClient("127.0.0.1:18766", "tok");

  it("sends the bearer token and parses JSON", async () => {
    fetchMock.mockResolvedValue(json([{ name: "a" }]));
    const r = await c.machines();
    expect(r).toEqual([{ name: "a" }]);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://127.0.0.1:18766/api/v1/machines");
    expect(init.headers.Authorization).toBe("Bearer tok");
  });

  it("encodes machine names in paths", async () => {
    fetchMock.mockResolvedValue(json({}));
    await c.connect("a b/c");
    expect(fetchMock.mock.calls[0][0]).toContain("/machines/a%20b%2Fc/connect");
    expect(fetchMock.mock.calls[0][1].method).toBe("POST");
  });

  it("serializes JSON bodies with a content type", async () => {
    fetchMock.mockResolvedValue(json({}));
    await c.setControl("m", "human");
    const init = fetchMock.mock.calls[0][1];
    expect(init.method).toBe("PUT");
    expect(init.headers["Content-Type"]).toBe("application/json");
    expect(JSON.parse(init.body)).toEqual({ mode: "human" });
  });

  it("passes delete_secret only when requested", async () => {
    fetchMock.mockImplementation(async () => json({ status: "ok" }));
    await c.deleteMachine("m");
    await c.deleteMachine("m", true);
    expect(fetchMock.mock.calls[0][0]).toMatch(/\/machines\/m$/);
    expect(fetchMock.mock.calls[1][0]).toMatch(/\/machines\/m\?delete_secret=true$/);
  });

  it("turns API errors into ApiError with code and message", async () => {
    fetchMock.mockResolvedValue(json({ error: { code: "conflict", message: "machine exists" } }, 409));
    await expect(c.createMachine({ name: "a", host: "h", username: "u" })).rejects.toMatchObject({
      name: "ApiError",
      status: 409,
      code: "conflict",
      message: "machine exists",
    });
  });

  it("reports network failures as status 0", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    const e = await c.health().catch((x) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect(e.status).toBe(0);
  });

  it("reads the screen size header of screenshots", async () => {
    fetchMock.mockResolvedValue(new Response(new Blob(["x"]), { headers: { "X-Screen-Size": "1920x1080" } }));
    const r = await c.screenshot("m", { maxWidth: 400, format: "jpeg" });
    expect(r.screen).toEqual([1920, 1080]);
    expect(fetchMock.mock.calls[0][0]).toContain("max_width=400");
    expect(fetchMock.mock.calls[0][0]).toContain("format=jpeg");
  });

  it("sends raw YAML with a yaml content type", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 200 }));
    await c.putConfigRaw("machines: []\n");
    const init = fetchMock.mock.calls[0][1];
    expect(init.body).toBe("machines: []\n");
    expect(init.headers["Content-Type"]).toBe("application/yaml");
  });

  it("builds token URLs for thumbnails", () => {
    expect(c.thumbUrl("/api/v1/audit/thumbs/x.jpg")).toBe("http://127.0.0.1:18766/api/v1/audit/thumbs/x.jpg?token=tok");
  });
});

describe("LiveStream.url", () => {
  it("builds a ws URL with query params and token", () => {
    const c = new ApiClient("127.0.0.1:18766", "tok");
    const u = new URL(LiveStream.url(c, "win 1", { fps: 15, maxWidth: 1600, quality: 70, connect: false }));
    expect(u.protocol).toBe("ws:");
    expect(u.pathname).toBe("/api/v1/machines/win%201/stream");
    expect(u.searchParams.get("fps")).toBe("15");
    expect(u.searchParams.get("connect")).toBe("false");
    expect(u.searchParams.get("token")).toBe("tok");
  });
});
