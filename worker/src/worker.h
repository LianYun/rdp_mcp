/* rdp-worker: headless FreeRDP session controlled over a unix socket.
 * See docs/worker-ipc.md for the protocol. */
#ifndef RDP_WORKER_H
#define RDP_WORKER_H

#include <freerdp/freerdp.h>
#include <freerdp/client.h>
#include <freerdp/client/cliprdr.h>
#include <freerdp/client/disp.h>
#include <winpr/json.h>
#include <winpr/synch.h>

#define TAG "rdp-worker"

typedef struct
{
	BOOL visible;
	INT32 x, y;           /* position in desktop coordinates */
	UINT32 hot_x, hot_y;  /* hotspot of current shape */
	UINT32 width, height; /* size of current shape (0 = default arrow) */
	BYTE* bgra;           /* current shape, BGRA32, width*4 stride; NULL = default arrow */
	UINT32 shape_seq;     /* incremented on every shape change */
	BOOL changed;         /* position/shape changed since last event */
} wCursor;

typedef struct
{
	rdpClientContext common;

	/* IPC */
	int ipc_fd;
	HANDLE ipc_event;
	BYTE* rbuf;
	size_t rlen;
	size_t rcap;

	/* main loop */
	HANDLE wake; /* signaled from other threads */
	BOOL connected;
	BOOL quit;

	/* framebuffer change tracking (written from paint threads) */
	CRITICAL_SECTION state_lock;
	UINT32 dirty_seq;
	UINT64 last_change_ms;
	BOOL subscribed;
	UINT32 sent_dirty_seq;
	UINT64 last_dirty_event_ms;
	UINT32 known_width, known_height;

	/* pointer */
	wCursor cursor;
	UINT64 last_cursor_event_ms;

	/* certificate decision */
	int cert_mode; /* 0 ask service, 1 ignore */

	/* clipboard (callbacks run on channel thread; guarded by clip_lock) */
	CRITICAL_SECTION clip_lock;
	CliprdrClientContext* clip;
	BOOL clip_ready;
	BOOL server_has_text;
	BYTE* local_text; /* UTF-16LE including terminating NUL */
	size_t local_text_len;
	BYTE* recv_data;
	size_t recv_len;
	BOOL recv_done;
	BOOL recv_failed;
	INT64 pending_get_id; /* -1 if none */
	UINT64 pending_get_deadline;

	/* display control */
	DispClientContext* disp;
} wContext;

UINT64 now_ms(void);

/* ipc.c */
BOOL ipc_open(wContext* wc, const char* path);
BOOL ipc_send_json(wContext* wc, WINPR_JSON* msg); /* takes ownership */
BOOL ipc_send_frame(wContext* wc, UINT32 width, UINT32 height, UINT32 req_id, const BYTE* data,
                    UINT32 src_stride);
/* Reads available data; returns parsed JSON message or NULL. *eof set on EOF/error. */
WINPR_JSON* ipc_next_message(wContext* wc, BOOL blocking, BOOL* eof);
WINPR_JSON* resp_new(INT64 id, BOOL ok);
BOOL resp_ok(wContext* wc, INT64 id);
BOOL resp_err(wContext* wc, INT64 id, const char* fmt, ...);
BOOL send_event(wContext* wc, WINPR_JSON* ev); /* takes ownership */
INT64 json_int(WINPR_JSON* obj, const char* key, INT64 def);
BOOL json_bool(WINPR_JSON* obj, const char* key, BOOL def);
const char* json_str(WINPR_JSON* obj, const char* key);

/* rdp.c */
int rdp_client_entry(RDP_CLIENT_ENTRY_POINTS* ep);
BOOL rdp_handle_request(wContext* wc, WINPR_JSON* req);
void rdp_mark_dirty(wContext* wc);

/* clipboard.c */
void clip_attach(wContext* wc, CliprdrClientContext* clip);
void clip_detach(wContext* wc);
BOOL clip_get(wContext* wc, INT64 id, UINT32 timeout_ms);
BOOL clip_set(wContext* wc, INT64 id, const char* text);
void clip_poll(wContext* wc); /* main thread: deliver pending results / timeouts */

/* pointer.c */
BOOL pointer_register(rdpContext* context);
void pointer_set_position(wContext* wc, INT32 x, INT32 y);

#endif
