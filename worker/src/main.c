#include "worker.h"

#include <signal.h>
#include <stdio.h>
#include <string.h>

#include <freerdp/client/cmdline.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/log.h>
#include <freerdp/version.h>
#include <freerdp/utils/helpers.h>

#define DIRTY_EVENT_INTERVAL_MS 33
#define CURSOR_EVENT_INTERVAL_MS 33

static void usage(const char* argv0)
{
	(void)fprintf(stderr, "usage: %s --ipc <socket-path> | --version\n", argv0);
}

static void emit_frame_dirty(wContext* wc, UINT64 now)
{
	EnterCriticalSection(&wc->state_lock);
	const BOOL sub = wc->subscribed;
	const UINT32 seq = wc->dirty_seq;
	LeaveCriticalSection(&wc->state_lock);
	if (!sub || seq == wc->sent_dirty_seq)
		return;
	if (now - wc->last_dirty_event_ms < DIRTY_EVENT_INTERVAL_MS)
		return;
	wc->sent_dirty_seq = seq;
	wc->last_dirty_event_ms = now;
	WINPR_JSON* ev = WINPR_JSON_CreateObject();
	(void)WINPR_JSON_AddStringToObject(ev, "event", "frame_dirty");
	(void)WINPR_JSON_AddIntegerToObject(ev, "dirty_seq", seq);
	(void)send_event(wc, ev);
}

static void emit_cursor(wContext* wc, UINT64 now)
{
	if (!wc->cursor.changed || now - wc->last_cursor_event_ms < CURSOR_EVENT_INTERVAL_MS)
		return;
	wc->cursor.changed = FALSE;
	wc->last_cursor_event_ms = now;
	WINPR_JSON* ev = WINPR_JSON_CreateObject();
	(void)WINPR_JSON_AddStringToObject(ev, "event", "cursor");
	(void)WINPR_JSON_AddIntegerToObject(ev, "x", wc->cursor.x);
	(void)WINPR_JSON_AddIntegerToObject(ev, "y", wc->cursor.y);
	(void)WINPR_JSON_AddBoolToObject(ev, "visible", wc->cursor.visible);
	(void)WINPR_JSON_AddIntegerToObject(ev, "shape_seq", wc->cursor.shape_seq);
	(void)send_event(wc, ev);
}

static void emit_resize_if_changed(wContext* wc)
{
	rdpGdi* gdi = wc->common.context.gdi;
	if (!wc->connected || !gdi)
		return;
	if ((UINT32)gdi->width == wc->known_width && (UINT32)gdi->height == wc->known_height)
		return;
	wc->known_width = gdi->width;
	wc->known_height = gdi->height;
	WINPR_JSON* ev = WINPR_JSON_CreateObject();
	(void)WINPR_JSON_AddStringToObject(ev, "event", "resized");
	(void)WINPR_JSON_AddIntegerToObject(ev, "width", gdi->width);
	(void)WINPR_JSON_AddIntegerToObject(ev, "height", gdi->height);
	(void)send_event(wc, ev);
}

static void emit_disconnected(wContext* wc)
{
	rdpContext* context = &wc->common.context;
	const UINT32 code = freerdp_get_last_error(context);
	WINPR_JSON* ev = WINPR_JSON_CreateObject();
	(void)WINPR_JSON_AddStringToObject(ev, "event", "disconnected");
	(void)WINPR_JSON_AddIntegerToObject(ev, "code", code);
	(void)WINPR_JSON_AddStringToObject(ev, "reason", freerdp_get_last_error_string(code));
	(void)send_event(wc, ev);
}

static BOOL process_ipc(wContext* wc)
{
	for (;;)
	{
		BOOL eof = FALSE;
		WINPR_JSON* msg = ipc_next_message(wc, FALSE, &eof);
		if (!msg)
			return !eof;
		const BOOL ok = rdp_handle_request(wc, msg);
		WINPR_JSON_Delete(msg);
		if (!ok)
			return FALSE;
		if (wc->quit)
			return TRUE;
	}
}

static int run(wContext* wc)
{
	rdpContext* context = &wc->common.context;
	HANDLE handles[MAXIMUM_WAIT_OBJECTS] = { 0 };

	while (!wc->quit)
	{
		DWORD n = 0;
		if (wc->connected)
		{
			n = freerdp_get_event_handles(context, handles, ARRAYSIZE(handles) - 2);
			if (n == 0)
			{
				WLog_ERR(TAG, "freerdp_get_event_handles failed");
				break;
			}
		}
		handles[n++] = wc->ipc_event;
		handles[n++] = wc->wake;

		DWORD timeout = INFINITE;
		if (wc->pending_get_id >= 0 || wc->subscribed || wc->cursor.changed)
			timeout = DIRTY_EVENT_INTERVAL_MS;

		const DWORD status = WaitForMultipleObjects(n, handles, FALSE, timeout);
		if (status == WAIT_FAILED)
		{
			WLog_ERR(TAG, "WaitForMultipleObjects failed");
			break;
		}

		if (wc->connected)
		{
			if (!freerdp_check_event_handles(context) ||
			    freerdp_shall_disconnect_context(context))
			{
				emit_disconnected(wc);
				break;
			}
		}

		if (!process_ipc(wc))
			break; /* service went away */

		(void)ResetEvent(wc->wake);
		const UINT64 now = now_ms();
		clip_poll(wc);
		emit_resize_if_changed(wc);
		emit_frame_dirty(wc, now);
		emit_cursor(wc, now);
	}

	if (wc->connected)
	{
		freerdp_disconnect(context->instance);
		wc->connected = FALSE;
	}
	return 0;
}

int main(int argc, char* argv[])
{
	const char* ipc_path = NULL;
	for (int i = 1; i < argc; i++)
	{
		if (strcmp(argv[i], "--ipc") == 0 && i + 1 < argc)
			ipc_path = argv[++i];
		else if (strcmp(argv[i], "--version") == 0)
		{
			printf("rdp-worker 0.1.0 (FreeRDP %s)\n", freerdp_get_version_string());
			return 0;
		}
	}
	if (!ipc_path)
	{
		usage(argv[0]);
		return 2;
	}
	(void)signal(SIGPIPE, SIG_IGN);

	if (!freerdp_setApplicationDetails("rdp_mcp", "rdp-worker", 3))
		return 1;

	RDP_CLIENT_ENTRY_POINTS ep = { 0 };
	rdp_client_entry(&ep);
	rdpContext* context = freerdp_client_context_new(&ep);
	if (!context)
		return 1;
	wContext* wc = (wContext*)context;
	wc->ipc_fd = -1;
	wc->pending_get_id = -1;
	InitializeCriticalSection(&wc->state_lock);
	InitializeCriticalSection(&wc->clip_lock);
	wc->wake = CreateEvent(NULL, TRUE, FALSE, NULL);

	int rc = 1;
	if (wc->wake && ipc_open(wc, ipc_path))
	{
		WINPR_JSON* hello = WINPR_JSON_CreateObject();
		(void)WINPR_JSON_AddStringToObject(hello, "event", "hello");
		(void)WINPR_JSON_AddStringToObject(hello, "version", "0.1.0");
		(void)WINPR_JSON_AddStringToObject(hello, "freerdp", freerdp_get_version_string());
		(void)WINPR_JSON_AddIntegerToObject(hello, "protocol", 1);
		if (send_event(wc, hello))
			rc = run(wc);
	}

	(void)freerdp_client_stop(context);
	if (wc->wake)
		(void)CloseHandle(wc->wake);
	if (wc->ipc_event)
		(void)CloseHandle(wc->ipc_event);
	free(wc->rbuf);
	free(wc->local_text);
	free(wc->recv_data);
	DeleteCriticalSection(&wc->state_lock);
	DeleteCriticalSection(&wc->clip_lock);
	freerdp_client_context_free(context);
	return rc;
}
