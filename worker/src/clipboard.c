#include "worker.h"

#include <string.h>

#include <freerdp/channels/cliprdr.h>
#include <winpr/clipboard.h>
#include <winpr/string.h>
#include <winpr/user.h>

#ifndef CF_UNICODETEXT
#define CF_UNICODETEXT 13
#endif
#ifndef CF_TEXT
#define CF_TEXT 1
#endif

static wContext* ctx_of(CliprdrClientContext* clip)
{
	return (wContext*)clip->custom;
}

static UINT send_format_list(wContext* wc, CliprdrClientContext* clip, BOOL with_text)
{
	CLIPRDR_FORMAT fmt = { .formatId = CF_UNICODETEXT, .formatName = NULL };
	CLIPRDR_FORMAT_LIST list = { 0 };
	list.common.msgType = CB_FORMAT_LIST;
	list.numFormats = with_text ? 1 : 0;
	list.formats = with_text ? &fmt : NULL;
	return clip->ClientFormatList(clip, &list);
}

static UINT cb_server_capabilities(CliprdrClientContext* clip, const CLIPRDR_CAPABILITIES* caps)
{
	return CHANNEL_RC_OK;
}

static UINT cb_monitor_ready(CliprdrClientContext* clip, const CLIPRDR_MONITOR_READY* ready)
{
	wContext* wc = ctx_of(clip);
	CLIPRDR_GENERAL_CAPABILITY_SET general = { 0 };
	general.capabilitySetType = CB_CAPSTYPE_GENERAL;
	general.capabilitySetLength = 12;
	general.version = CB_CAPS_VERSION_2;
	general.generalFlags = CB_USE_LONG_FORMAT_NAMES;
	CLIPRDR_CAPABILITIES caps = { 0 };
	caps.common.msgType = CB_CLIP_CAPS;
	caps.cCapabilitiesSets = 1;
	caps.capabilitySets = (CLIPRDR_CAPABILITY_SET*)&general;
	UINT rc = clip->ClientCapabilities(clip, &caps);
	if (rc != CHANNEL_RC_OK)
		return rc;

	EnterCriticalSection(&wc->clip_lock);
	const BOOL have = wc->local_text != NULL;
	wc->clip_ready = TRUE;
	LeaveCriticalSection(&wc->clip_lock);
	return send_format_list(wc, clip, have);
}

static UINT cb_server_format_list(CliprdrClientContext* clip, const CLIPRDR_FORMAT_LIST* list)
{
	wContext* wc = ctx_of(clip);
	BOOL has_text = FALSE;
	for (UINT32 i = 0; i < list->numFormats; i++)
	{
		if (list->formats[i].formatId == CF_UNICODETEXT || list->formats[i].formatId == CF_TEXT)
			has_text = TRUE;
	}
	EnterCriticalSection(&wc->clip_lock);
	wc->server_has_text = has_text;
	/* remote side owns the clipboard now */
	free(wc->local_text);
	wc->local_text = NULL;
	wc->local_text_len = 0;
	LeaveCriticalSection(&wc->clip_lock);

	CLIPRDR_FORMAT_LIST_RESPONSE resp = { 0 };
	resp.common.msgType = CB_FORMAT_LIST_RESPONSE;
	resp.common.msgFlags = CB_RESPONSE_OK;
	return clip->ClientFormatListResponse(clip, &resp);
}

static UINT cb_server_format_list_response(CliprdrClientContext* clip,
                                           const CLIPRDR_FORMAT_LIST_RESPONSE* resp)
{
	return CHANNEL_RC_OK;
}

static UINT cb_server_format_data_request(CliprdrClientContext* clip,
                                          const CLIPRDR_FORMAT_DATA_REQUEST* req)
{
	wContext* wc = ctx_of(clip);
	CLIPRDR_FORMAT_DATA_RESPONSE resp = { 0 };
	resp.common.msgType = CB_FORMAT_DATA_RESPONSE;
	BYTE* copy = NULL;

	EnterCriticalSection(&wc->clip_lock);
	if (req->requestedFormatId == CF_UNICODETEXT && wc->local_text)
	{
		copy = malloc(wc->local_text_len);
		if (copy)
		{
			memcpy(copy, wc->local_text, wc->local_text_len);
			resp.common.dataLen = (UINT32)wc->local_text_len;
		}
	}
	LeaveCriticalSection(&wc->clip_lock);

	resp.common.msgFlags = copy ? CB_RESPONSE_OK : CB_RESPONSE_FAIL;
	resp.requestedFormatData = copy;
	UINT rc = clip->ClientFormatDataResponse(clip, &resp);
	free(copy);
	return rc;
}

static UINT cb_server_format_data_response(CliprdrClientContext* clip,
                                           const CLIPRDR_FORMAT_DATA_RESPONSE* resp)
{
	wContext* wc = ctx_of(clip);
	EnterCriticalSection(&wc->clip_lock);
	free(wc->recv_data);
	wc->recv_data = NULL;
	wc->recv_len = 0;
	wc->recv_failed = (resp->common.msgFlags & CB_RESPONSE_FAIL) != 0;
	if (!wc->recv_failed && resp->common.dataLen > 0 && resp->requestedFormatData)
	{
		wc->recv_data = malloc(resp->common.dataLen);
		if (wc->recv_data)
		{
			memcpy(wc->recv_data, resp->requestedFormatData, resp->common.dataLen);
			wc->recv_len = resp->common.dataLen;
		}
	}
	wc->recv_done = TRUE;
	LeaveCriticalSection(&wc->clip_lock);
	(void)SetEvent(wc->wake);
	return CHANNEL_RC_OK;
}

void clip_attach(wContext* wc, CliprdrClientContext* clip)
{
	if (!clip)
		return;
	clip->custom = wc;
	clip->ServerCapabilities = cb_server_capabilities;
	clip->MonitorReady = cb_monitor_ready;
	clip->ServerFormatList = cb_server_format_list;
	clip->ServerFormatListResponse = cb_server_format_list_response;
	clip->ServerFormatDataRequest = cb_server_format_data_request;
	clip->ServerFormatDataResponse = cb_server_format_data_response;
	EnterCriticalSection(&wc->clip_lock);
	wc->clip = clip;
	LeaveCriticalSection(&wc->clip_lock);
}

void clip_detach(wContext* wc)
{
	EnterCriticalSection(&wc->clip_lock);
	if (wc->clip)
		wc->clip->custom = NULL;
	wc->clip = NULL;
	wc->clip_ready = FALSE;
	LeaveCriticalSection(&wc->clip_lock);
}

BOOL clip_get(wContext* wc, INT64 id, UINT32 timeout_ms)
{
	EnterCriticalSection(&wc->clip_lock);
	CliprdrClientContext* clip = wc->clip;
	const BOOL ready = wc->clip_ready;
	const BOOL has_text = wc->server_has_text;
	BYTE* local = NULL;
	size_t local_len = 0;
	if (wc->local_text)
	{
		local = malloc(wc->local_text_len);
		if (local)
		{
			memcpy(local, wc->local_text, wc->local_text_len);
			local_len = wc->local_text_len;
		}
	}
	LeaveCriticalSection(&wc->clip_lock);

	if (!clip || !ready)
	{
		free(local);
		return resp_err(wc, id, "clipboard channel not available");
	}
	if (local)
	{
		/* we own the clipboard: return what we set */
		char* utf8 = ConvertWCharNToUtf8Alloc((const WCHAR*)local, local_len / sizeof(WCHAR), NULL);
		free(local);
		WINPR_JSON* r = resp_new(id, TRUE);
		(void)WINPR_JSON_AddStringToObject(r, "text", utf8 ? utf8 : "");
		free(utf8);
		return ipc_send_json(wc, r);
	}
	if (!has_text)
	{
		WINPR_JSON* r = resp_new(id, TRUE);
		(void)WINPR_JSON_AddStringToObject(r, "text", "");
		(void)WINPR_JSON_AddBoolToObject(r, "empty", TRUE);
		return ipc_send_json(wc, r);
	}
	if (wc->pending_get_id >= 0)
		return resp_err(wc, id, "another clipboard request is pending");

	EnterCriticalSection(&wc->clip_lock);
	wc->recv_done = FALSE;
	LeaveCriticalSection(&wc->clip_lock);

	CLIPRDR_FORMAT_DATA_REQUEST req = { 0 };
	req.common.msgType = CB_FORMAT_DATA_REQUEST;
	req.requestedFormatId = CF_UNICODETEXT;
	if (clip->ClientFormatDataRequest(clip, &req) != CHANNEL_RC_OK)
		return resp_err(wc, id, "clipboard data request failed");
	wc->pending_get_id = id;
	wc->pending_get_deadline = now_ms() + timeout_ms;
	return TRUE;
}

void clip_poll(wContext* wc)
{
	if (wc->pending_get_id < 0)
		return;
	EnterCriticalSection(&wc->clip_lock);
	const BOOL done = wc->recv_done;
	BYTE* data = wc->recv_data;
	size_t len = wc->recv_len;
	BOOL failed = wc->recv_failed;
	if (done)
	{
		wc->recv_data = NULL;
		wc->recv_len = 0;
		wc->recv_done = FALSE;
	}
	LeaveCriticalSection(&wc->clip_lock);

	const INT64 id = wc->pending_get_id;
	if (done)
	{
		wc->pending_get_id = -1;
		if (failed)
		{
			free(data);
			(void)resp_err(wc, id, "remote clipboard returned failure");
			return;
		}
		size_t wlen = len / sizeof(WCHAR);
		const WCHAR* w = (const WCHAR*)data;
		while (wlen > 0 && w[wlen - 1] == 0)
			wlen--;
		char* utf8 = data ? ConvertWCharNToUtf8Alloc(w, wlen, NULL) : NULL;
		free(data);
		WINPR_JSON* r = resp_new(id, TRUE);
		(void)WINPR_JSON_AddStringToObject(r, "text", utf8 ? utf8 : "");
		free(utf8);
		(void)ipc_send_json(wc, r);
		return;
	}
	if (now_ms() >= wc->pending_get_deadline)
	{
		wc->pending_get_id = -1;
		(void)resp_err(wc, id, "clipboard request timed out");
	}
}

BOOL clip_set(wContext* wc, INT64 id, const char* text)
{
	if (!text)
		return resp_err(wc, id, "text required");
	size_t wlen = 0;
	WCHAR* w = ConvertUtf8ToWCharAlloc(text, &wlen);
	if (!w)
		return resp_err(wc, id, "invalid utf-8");

	EnterCriticalSection(&wc->clip_lock);
	CliprdrClientContext* clip = wc->clip;
	const BOOL ready = wc->clip_ready;
	if (clip && ready)
	{
		free(wc->local_text);
		wc->local_text = (BYTE*)w;
		wc->local_text_len = (wlen + 1) * sizeof(WCHAR);
		w = NULL;
	}
	LeaveCriticalSection(&wc->clip_lock);
	free(w);

	if (!clip || !ready)
		return resp_err(wc, id, "clipboard channel not available");
	if (send_format_list(wc, clip, TRUE) != CHANNEL_RC_OK)
		return resp_err(wc, id, "failed to announce clipboard");
	return resp_ok(wc, id);
}
