#include "worker.h"

#include <string.h>

#include <freerdp/channels/channels.h>
#include <freerdp/channels/disp.h>
#include <freerdp/client/channels.h>
#include <freerdp/client/cmdline.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/input.h>
#include <freerdp/log.h>
#include <freerdp/scancode.h>
#include <freerdp/settings.h>
#include <winpr/string.h>
#include <winpr/sysinfo.h>

UINT64 now_ms(void)
{
	return GetTickCount64();
}

void rdp_mark_dirty(wContext* wc)
{
	EnterCriticalSection(&wc->state_lock);
	wc->dirty_seq++;
	wc->last_change_ms = now_ms();
	const BOOL notify = wc->subscribed;
	LeaveCriticalSection(&wc->state_lock);
	if (notify)
		(void)SetEvent(wc->wake);
}

/* ---- update callbacks ---------------------------------------------------- */

static BOOL w_begin_paint(rdpContext* context)
{
	rdpGdi* gdi = context->gdi;
	if (gdi && gdi->primary && gdi->primary->hdc && gdi->primary->hdc->hwnd &&
	    gdi->primary->hdc->hwnd->invalid)
		gdi->primary->hdc->hwnd->invalid->null = TRUE;
	return TRUE;
}

static BOOL w_end_paint(rdpContext* context)
{
	rdpGdi* gdi = context->gdi;
	if (!gdi || !gdi->primary || !gdi->primary->hdc || !gdi->primary->hdc->hwnd)
		return TRUE;
	HGDI_WND hwnd = gdi->primary->hdc->hwnd;
	if (!hwnd->invalid || hwnd->invalid->null)
		return TRUE;
	rdp_mark_dirty((wContext*)context);
	return TRUE;
}

static BOOL w_desktop_resize(rdpContext* context)
{
	rdpSettings* settings = context->settings;
	if (!gdi_resize(context->gdi, freerdp_settings_get_uint32(settings, FreeRDP_DesktopWidth),
	                freerdp_settings_get_uint32(settings, FreeRDP_DesktopHeight)))
		return FALSE;
	rdp_mark_dirty((wContext*)context);
	(void)SetEvent(((wContext*)context)->wake);
	return TRUE;
}

static BOOL w_play_sound(rdpContext* context, const PLAY_SOUND_UPDATE* play_sound)
{
	return TRUE;
}

static BOOL w_keyboard_set_indicators(rdpContext* context, UINT16 led_flags)
{
	return TRUE;
}

static BOOL w_keyboard_set_ime_status(rdpContext* context, UINT16 imeId, UINT32 imeState,
                                      UINT32 imeConvMode)
{
	return TRUE;
}

/* ---- channels -------------------------------------------------------------- */

static void w_channel_connected(void* context, const ChannelConnectedEventArgs* e)
{
	wContext* wc = (wContext*)context;
	if (strcmp(e->name, CLIPRDR_SVC_CHANNEL_NAME) == 0)
		clip_attach(wc, (CliprdrClientContext*)e->pInterface);
	else if (strcmp(e->name, DISP_DVC_CHANNEL_NAME) == 0)
	{
		wc->disp = (DispClientContext*)e->pInterface;
		if (wc->disp)
			wc->disp->custom = wc;
	}
	else
		freerdp_client_OnChannelConnectedEventHandler(&wc->common, e);
}

static void w_channel_disconnected(void* context, const ChannelDisconnectedEventArgs* e)
{
	wContext* wc = (wContext*)context;
	if (strcmp(e->name, CLIPRDR_SVC_CHANNEL_NAME) == 0)
		clip_detach(wc);
	else if (strcmp(e->name, DISP_DVC_CHANNEL_NAME) == 0)
		wc->disp = NULL;
	else
		freerdp_client_OnChannelDisconnectedEventHandler(&wc->common, e);
}

/* ---- connection callbacks --------------------------------------------------- */

static BOOL w_pre_connect(freerdp* instance)
{
	rdpContext* context = instance->context;
	rdpSettings* settings = context->settings;
	if (!freerdp_settings_set_uint32(settings, FreeRDP_OsMajorType, OSMAJORTYPE_UNIX))
		return FALSE;
	if (!freerdp_settings_set_uint32(settings, FreeRDP_OsMinorType, OSMINORTYPE_NATIVE_XSERVER))
		return FALSE;
	if (PubSub_SubscribeChannelConnected(context->pubSub, w_channel_connected) < 0)
		return FALSE;
	if (PubSub_SubscribeChannelDisconnected(context->pubSub, w_channel_disconnected) < 0)
		return FALSE;
	return TRUE;
}

static BOOL w_post_connect(freerdp* instance)
{
	rdpContext* context = instance->context;
	if (!gdi_init(instance, PIXEL_FORMAT_BGRA32))
		return FALSE;
	if (!pointer_register(context))
		return FALSE;
	rdpUpdate* update = context->update;
	update->BeginPaint = w_begin_paint;
	update->EndPaint = w_end_paint;
	update->PlaySound = w_play_sound;
	update->DesktopResize = w_desktop_resize;
	update->SetKeyboardIndicators = w_keyboard_set_indicators;
	update->SetKeyboardImeStatus = w_keyboard_set_ime_status;
	return TRUE;
}

static void w_post_disconnect(freerdp* instance)
{
	if (!instance || !instance->context)
		return;
	PubSub_UnsubscribeChannelConnected(instance->context->pubSub, w_channel_connected);
	PubSub_UnsubscribeChannelDisconnected(instance->context->pubSub, w_channel_disconnected);
	gdi_free(instance);
}

static BOOL w_authenticate_ex(freerdp* instance, char** username, char** password, char** domain,
                              rdp_auth_reason reason)
{
	/* never prompt: credentials come from the service */
	return (username && *username && password && *password) ? TRUE : FALSE;
}

/* Ask the service to decide; blocks until a cert_reply arrives. */
static DWORD ask_cert(wContext* wc, const char* host, UINT16 port, const char* common_name,
                      const char* subject, const char* issuer, const char* fingerprint,
                      const char* old_fingerprint, DWORD flags)
{
	if (wc->cert_mode == 1)
		return 2;
	WINPR_JSON* ev = WINPR_JSON_CreateObject();
	(void)WINPR_JSON_AddStringToObject(ev, "event", "cert_verify");
	(void)WINPR_JSON_AddStringToObject(ev, "host", host ? host : "");
	(void)WINPR_JSON_AddIntegerToObject(ev, "port", port);
	(void)WINPR_JSON_AddStringToObject(ev, "common_name", common_name ? common_name : "");
	(void)WINPR_JSON_AddStringToObject(ev, "subject", subject ? subject : "");
	(void)WINPR_JSON_AddStringToObject(ev, "issuer", issuer ? issuer : "");
	(void)WINPR_JSON_AddStringToObject(ev, "fingerprint", fingerprint ? fingerprint : "");
	(void)WINPR_JSON_AddBoolToObject(ev, "changed", old_fingerprint != NULL);
	(void)WINPR_JSON_AddBoolToObject(ev, "mismatch", (flags & VERIFY_CERT_FLAG_MISMATCH) != 0);
	if (!send_event(wc, ev))
		return 0;

	for (;;)
	{
		BOOL eof = FALSE;
		WINPR_JSON* msg = ipc_next_message(wc, TRUE, &eof);
		if (!msg)
		{
			if (eof)
			{
				wc->quit = TRUE;
				return 0;
			}
			continue;
		}
		const char* op = json_str(msg, "op");
		INT64 id = json_int(msg, "id", -1);
		if (op && strcmp(op, "cert_reply") == 0)
		{
			INT64 accept = json_int(msg, "accept", 0);
			(void)resp_ok(wc, id);
			WINPR_JSON_Delete(msg);
			/* never let FreeRDP persist (1); the service keeps its own known_hosts */
			return accept ? 2 : 0;
		}
		(void)resp_err(wc, id, "busy: waiting for certificate decision");
		WINPR_JSON_Delete(msg);
	}
}

static DWORD w_verify_certificate_ex(freerdp* instance, const char* host, UINT16 port,
                                     const char* common_name, const char* subject,
                                     const char* issuer, const char* fingerprint, DWORD flags)
{
	return ask_cert((wContext*)instance->context, host, port, common_name, subject, issuer,
	                fingerprint, NULL, flags);
}

static DWORD w_verify_changed_certificate_ex(freerdp* instance, const char* host, UINT16 port,
                                             const char* common_name, const char* subject,
                                             const char* issuer, const char* new_fingerprint,
                                             const char* old_subject, const char* old_issuer,
                                             const char* old_fingerprint, DWORD flags)
{
	return ask_cert((wContext*)instance->context, host, port, common_name, subject, issuer,
	                new_fingerprint, old_fingerprint ? old_fingerprint : "", flags);
}

static int w_logon_error_info(freerdp* instance, UINT32 data, UINT32 type)
{
	WLog_INFO(TAG, "logon error info %s [%s]", freerdp_get_logon_error_info_data(data),
	          freerdp_get_logon_error_info_type(type));
	return 1;
}

static BOOL w_present_gateway_message(freerdp* instance, UINT32 type, BOOL isDisplayMandatory,
                                      BOOL isConsentMandatory, size_t length, const WCHAR* message)
{
	return TRUE;
}

static BOOL w_save_session_info(rdpContext* context, UINT32 type, const void* data)
{
	wContext* wc = (wContext*)context;
	wc->logon_count++;
	wc->logon_ms = now_ms();
	WLog_INFO(TAG, "logon notification type %" PRIu32, type);
	return TRUE;
}

static BOOL w_client_new(freerdp* instance, rdpContext* context)
{
	context->update->SaveSessionInfo = w_save_session_info;
	instance->PreConnect = w_pre_connect;
	instance->PostConnect = w_post_connect;
	instance->PostDisconnect = w_post_disconnect;
	instance->AuthenticateEx = w_authenticate_ex;
	instance->VerifyCertificateEx = w_verify_certificate_ex;
	instance->VerifyChangedCertificateEx = w_verify_changed_certificate_ex;
	instance->LogonErrorInfo = w_logon_error_info;
	instance->PresentGatewayMessage = w_present_gateway_message;
	instance->GetAccessToken = client_failsafe_get_access_token;
	return TRUE;
}

static void w_client_free(freerdp* instance, rdpContext* context)
{
}

static int w_client_start(rdpContext* context)
{
	return 0;
}

static int w_client_stop(rdpContext* context)
{
	return 0;
}

int rdp_client_entry(RDP_CLIENT_ENTRY_POINTS* ep)
{
	ZeroMemory(ep, sizeof(RDP_CLIENT_ENTRY_POINTS));
	ep->Version = RDP_CLIENT_INTERFACE_VERSION;
	ep->Size = sizeof(RDP_CLIENT_ENTRY_POINTS_V1);
	ep->ContextSize = sizeof(wContext);
	ep->ClientNew = w_client_new;
	ep->ClientFree = w_client_free;
	ep->ClientStart = w_client_start;
	ep->ClientStop = w_client_stop;
	return 0;
}

/* ---- requests --------------------------------------------------------------- */

static BOOL set_str(rdpSettings* s, FreeRDP_Settings_Keys_String key, const char* v)
{
	return freerdp_settings_set_string(s, key, v);
}

static BOOL apply_connect_settings(wContext* wc, WINPR_JSON* req, char* err, size_t errlen)
{
	rdpSettings* s = wc->common.context.settings;
	const char* host = json_str(req, "host");
	const char* user = json_str(req, "username");
	const char* pass = json_str(req, "password");
	const char* domain = json_str(req, "domain");
	const char* security = json_str(req, "security");
	const char* config_dir = json_str(req, "config_dir");
	const char* drive_path = json_str(req, "drive_path");
	const char* drive_name = json_str(req, "drive_name");
	const char* cert = json_str(req, "cert");
	UINT32 width = (UINT32)json_int(req, "width", 1920);
	UINT32 height = (UINT32)json_int(req, "height", 1080);

	if (!host || !*host)
	{
		(void)snprintf(err, errlen, "host is required");
		return FALSE;
	}
	BOOL ok = TRUE;
	ok &= set_str(s, FreeRDP_ServerHostname, host);
	ok &= freerdp_settings_set_uint32(s, FreeRDP_ServerPort, (UINT32)json_int(req, "port", 3389));
	if (user && *user)
		ok &= set_str(s, FreeRDP_Username, user);
	if (pass && *pass)
		ok &= set_str(s, FreeRDP_Password, pass);
	if (domain && *domain)
		ok &= set_str(s, FreeRDP_Domain, domain);
	if (config_dir && *config_dir)
		ok &= set_str(s, FreeRDP_ConfigPath, config_dir);

	ok &= freerdp_settings_set_uint32(s, FreeRDP_DesktopWidth, width);
	ok &= freerdp_settings_set_uint32(s, FreeRDP_DesktopHeight, height);
	ok &= freerdp_settings_set_uint32(s, FreeRDP_ColorDepth, 32);
	ok &= freerdp_settings_set_uint32(s, FreeRDP_KeyboardLayout, 0x00000409);
	ok &= freerdp_settings_set_bool(s, FreeRDP_SupportGraphicsPipeline, TRUE);
	ok &= freerdp_settings_set_bool(s, FreeRDP_SupportDisplayControl, TRUE);
	ok &= freerdp_settings_set_bool(s, FreeRDP_RemoteFxCodec, TRUE);
	ok &= freerdp_settings_set_bool(s, FreeRDP_DynamicResolutionUpdate, TRUE);
	ok &= freerdp_settings_set_bool(s, FreeRDP_AutoReconnectionEnabled, FALSE);
	ok &= freerdp_settings_set_bool(s, FreeRDP_AllowFontSmoothing, TRUE);
	ok &= freerdp_settings_set_bool(s, FreeRDP_DisableWallpaper,
	                                json_bool(req, "disable_wallpaper", TRUE));
	ok &= freerdp_settings_set_uint32(s, FreeRDP_TcpConnectTimeout,
	                                  (UINT32)json_int(req, "connect_timeout_ms", 15000));
	ok &= freerdp_set_connection_type(s, CONNECTION_TYPE_LAN);

	if (security && strcmp(security, "nla") == 0)
	{
		ok &= freerdp_settings_set_bool(s, FreeRDP_NlaSecurity, TRUE);
		ok &= freerdp_settings_set_bool(s, FreeRDP_TlsSecurity, FALSE);
		ok &= freerdp_settings_set_bool(s, FreeRDP_RdpSecurity, FALSE);
	}
	else if (security && strcmp(security, "tls") == 0)
	{
		ok &= freerdp_settings_set_bool(s, FreeRDP_NlaSecurity, FALSE);
		ok &= freerdp_settings_set_bool(s, FreeRDP_TlsSecurity, TRUE);
		ok &= freerdp_settings_set_bool(s, FreeRDP_RdpSecurity, FALSE);
	}
	else if (security && strcmp(security, "rdp") == 0)
	{
		ok &= freerdp_settings_set_bool(s, FreeRDP_NlaSecurity, FALSE);
		ok &= freerdp_settings_set_bool(s, FreeRDP_TlsSecurity, FALSE);
		ok &= freerdp_settings_set_bool(s, FreeRDP_RdpSecurity, TRUE);
		ok &= freerdp_settings_set_bool(s, FreeRDP_UseRdpSecurityLayer, TRUE);
	}

	wc->cert_mode = (cert && strcmp(cert, "ignore") == 0) ? 1 : 0;
	if (wc->cert_mode == 1)
		ok &= freerdp_settings_set_bool(s, FreeRDP_IgnoreCertificate, TRUE);

	ok &= freerdp_settings_set_bool(s, FreeRDP_RedirectClipboard,
	                                json_bool(req, "clipboard", TRUE));

	if (drive_path && *drive_path)
	{
		const char* params[] = { "drive", (drive_name && *drive_name) ? drive_name : "mcp",
			                     drive_path };
		ok &= freerdp_settings_set_bool(s, FreeRDP_DeviceRedirection, TRUE);
		ok &= freerdp_client_add_device_channel(s, ARRAYSIZE(params), params);
	}

	if (!ok)
		(void)snprintf(err, errlen, "failed to apply settings");
	return ok;
}

static BOOL handle_connect(wContext* wc, INT64 id, WINPR_JSON* req)
{
	char err[256] = { 0 };
	rdpContext* context = &wc->common.context;
	if (wc->connected)
		return resp_err(wc, id, "already connected");
	if (!apply_connect_settings(wc, req, err, sizeof(err)))
	{
		wc->quit = TRUE;
		return resp_err(wc, id, "%s", err);
	}
	if (freerdp_client_start(context) != 0)
	{
		wc->quit = TRUE;
		return resp_err(wc, id, "freerdp_client_start failed");
	}
	if (!freerdp_connect(context->instance))
	{
		UINT32 code = freerdp_get_last_error(context);
		wc->quit = TRUE;
		WINPR_JSON* r = resp_new(id, FALSE);
		char buf[512];
		(void)snprintf(buf, sizeof(buf), "connect failed: %s (%s)",
		               freerdp_get_last_error_string(code), freerdp_get_last_error_name(code));
		(void)WINPR_JSON_AddStringToObject(r, "error", buf);
		(void)WINPR_JSON_AddIntegerToObject(r, "code", code);
		return ipc_send_json(wc, r);
	}
	wc->connected = TRUE;
	rdpGdi* gdi = context->gdi;
	wc->known_width = gdi->width;
	wc->known_height = gdi->height;
	WINPR_JSON* r = resp_new(id, TRUE);
	(void)WINPR_JSON_AddIntegerToObject(r, "width", gdi->width);
	(void)WINPR_JSON_AddIntegerToObject(r, "height", gdi->height);
	return ipc_send_json(wc, r);
}

static BOOL handle_capture(wContext* wc, INT64 id)
{
	rdpContext* context = &wc->common.context;
	rdpGdi* gdi = context->gdi;
	if (!wc->connected || !gdi || !gdi->primary_buffer)
		return resp_err(wc, id, "not connected");
	rdp_update_lock(context->update);
	const UINT32 w = gdi->width;
	const UINT32 h = gdi->height;
	BOOL rc = ipc_send_frame(wc, w, h, (UINT32)id, gdi->primary_buffer, gdi->stride);
	rdp_update_unlock(context->update);
	if (!rc)
		return FALSE;
	EnterCriticalSection(&wc->state_lock);
	UINT32 seq = wc->dirty_seq;
	LeaveCriticalSection(&wc->state_lock);
	WINPR_JSON* r = resp_new(id, TRUE);
	(void)WINPR_JSON_AddIntegerToObject(r, "width", w);
	(void)WINPR_JSON_AddIntegerToObject(r, "height", h);
	(void)WINPR_JSON_AddIntegerToObject(r, "dirty_seq", seq);
	return ipc_send_json(wc, r);
}

static BOOL handle_status(wContext* wc, INT64 id)
{
	rdpGdi* gdi = wc->common.context.gdi;
	EnterCriticalSection(&wc->state_lock);
	UINT32 seq = wc->dirty_seq;
	UINT64 last = wc->last_change_ms;
	LeaveCriticalSection(&wc->state_lock);
	WINPR_JSON* r = resp_new(id, TRUE);
	(void)WINPR_JSON_AddBoolToObject(r, "connected", wc->connected);
	(void)WINPR_JSON_AddIntegerToObject(r, "width", (wc->connected && gdi) ? gdi->width : 0);
	(void)WINPR_JSON_AddIntegerToObject(r, "height", (wc->connected && gdi) ? gdi->height : 0);
	(void)WINPR_JSON_AddIntegerToObject(r, "dirty_seq", seq);
	(void)WINPR_JSON_AddIntegerToObject(r, "idle_ms", last ? (INT64)(now_ms() - last) : -1);
	(void)WINPR_JSON_AddIntegerToObject(r, "logon_count", wc->logon_count);
	(void)WINPR_JSON_AddIntegerToObject(r, "logon_ms_ago",
	                                    wc->logon_ms ? (INT64)(now_ms() - wc->logon_ms) : -1);
	return ipc_send_json(wc, r);
}

static BOOL handle_cursor_get(wContext* wc, INT64 id, BOOL want_shape)
{
	wCursor c = wc->cursor; /* main thread owns pointer state */
	if (want_shape && c.bgra && c.width && c.height)
	{
		if (!ipc_send_frame(wc, c.width, c.height, (UINT32)id, c.bgra, c.width * 4))
			return FALSE;
	}
	WINPR_JSON* r = resp_new(id, TRUE);
	(void)WINPR_JSON_AddIntegerToObject(r, "x", c.x);
	(void)WINPR_JSON_AddIntegerToObject(r, "y", c.y);
	(void)WINPR_JSON_AddBoolToObject(r, "visible", c.visible);
	(void)WINPR_JSON_AddIntegerToObject(r, "hot_x", c.hot_x);
	(void)WINPR_JSON_AddIntegerToObject(r, "hot_y", c.hot_y);
	(void)WINPR_JSON_AddIntegerToObject(r, "width", c.bgra ? c.width : 0);
	(void)WINPR_JSON_AddIntegerToObject(r, "height", c.bgra ? c.height : 0);
	(void)WINPR_JSON_AddIntegerToObject(r, "shape_seq", c.shape_seq);
	(void)WINPR_JSON_AddBoolToObject(r, "has_shape", want_shape && c.bgra != NULL);
	return ipc_send_json(wc, r);
}

static BOOL send_unicode_text(rdpInput* input, const char* text)
{
	size_t len = 0;
	WCHAR* w = ConvertUtf8ToWCharAlloc(text, &len);
	if (!w)
		return FALSE;
	BOOL ok = TRUE;
	for (size_t i = 0; ok && i < len; i++)
	{
		const WCHAR ch = w[i];
		if (ch == L'\r')
			continue;
		if (ch == L'\n' || ch == L'\t')
		{
			const DWORD sc = (ch == L'\n') ? RDP_SCANCODE_RETURN : RDP_SCANCODE_TAB;
			ok &= freerdp_input_send_keyboard_event_ex(input, TRUE, FALSE, sc);
			ok &= freerdp_input_send_keyboard_event_ex(input, FALSE, FALSE, sc);
			continue;
		}
		ok &= freerdp_input_send_unicode_keyboard_event(input, 0, (UINT16)ch);
		ok &= freerdp_input_send_unicode_keyboard_event(input, KBD_FLAGS_RELEASE, (UINT16)ch);
	}
	free(w);
	return ok;
}

static BOOL handle_resize(wContext* wc, INT64 id, WINPR_JSON* req)
{
	UINT32 w = (UINT32)json_int(req, "width", 0);
	UINT32 h = (UINT32)json_int(req, "height", 0);
	if (!wc->disp)
		return resp_err(wc, id, "display control channel not available");
	if (w < 200 || h < 200 || w > 8192 || h > 8192)
		return resp_err(wc, id, "invalid size");
	w &= ~1u;
	DISPLAY_CONTROL_MONITOR_LAYOUT layout = { 0 };
	layout.Flags = DISPLAY_CONTROL_MONITOR_PRIMARY;
	layout.Width = w;
	layout.Height = h;
	layout.Orientation = ORIENTATION_LANDSCAPE;
	layout.DesktopScaleFactor = 100;
	layout.DeviceScaleFactor = 100;
	layout.PhysicalWidth = w * 254 / 960;
	layout.PhysicalHeight = h * 254 / 960;
	if (wc->disp->SendMonitorLayout(wc->disp, 1, &layout) != CHANNEL_RC_OK)
		return resp_err(wc, id, "SendMonitorLayout failed");
	return resp_ok(wc, id);
}

BOOL rdp_handle_request(wContext* wc, WINPR_JSON* req)
{
	const char* op = json_str(req, "op");
	const INT64 id = json_int(req, "id", -1);
	rdpInput* input = wc->common.context.input;
	if (!op)
		return resp_err(wc, id, "missing op");

	if (strcmp(op, "connect") == 0)
		return handle_connect(wc, id, req);
	if (strcmp(op, "status") == 0)
		return handle_status(wc, id);
	if (strcmp(op, "disconnect") == 0)
	{
		wc->quit = TRUE;
		return resp_ok(wc, id);
	}
	if (strcmp(op, "cert_reply") == 0)
		return resp_err(wc, id, "no certificate decision pending");

	if (!wc->connected)
		return resp_err(wc, id, "not connected");

	if (strcmp(op, "capture") == 0)
		return handle_capture(wc, id);
	if (strcmp(op, "cursor_get") == 0)
		return handle_cursor_get(wc, id, json_bool(req, "shape", FALSE));
	if (strcmp(op, "subscribe") == 0)
	{
		EnterCriticalSection(&wc->state_lock);
		wc->subscribed = json_bool(req, "on", TRUE);
		LeaveCriticalSection(&wc->state_lock);
		return resp_ok(wc, id);
	}
	if (strcmp(op, "mouse") == 0 || strcmp(op, "mouse_ex") == 0)
	{
		const UINT16 flags = (UINT16)json_int(req, "flags", 0);
		const INT64 x = json_int(req, "x", wc->cursor.x);
		const INT64 y = json_int(req, "y", wc->cursor.y);
		const UINT16 ux = (UINT16)(x < 0 ? 0 : (x > 0xFFFF ? 0xFFFF : x));
		const UINT16 uy = (UINT16)(y < 0 ? 0 : (y > 0xFFFF ? 0xFFFF : y));
		BOOL ok = (op[5] == '_') ? freerdp_input_send_extended_mouse_event(input, flags, ux, uy)
		                         : freerdp_input_send_mouse_event(input, flags, ux, uy);
		pointer_set_position(wc, ux, uy);
		return ok ? resp_ok(wc, id) : resp_err(wc, id, "send mouse event failed");
	}
	if (strcmp(op, "key") == 0)
	{
		const DWORD sc = MAKE_RDP_SCANCODE((DWORD)json_int(req, "scancode", 0),
		                                   json_bool(req, "extended", FALSE));
		BOOL ok = freerdp_input_send_keyboard_event_ex(input, json_bool(req, "down", TRUE),
		                                               json_bool(req, "repeat", FALSE), sc);
		return ok ? resp_ok(wc, id) : resp_err(wc, id, "send key event failed");
	}
	if (strcmp(op, "unicode") == 0)
	{
		const char* text = json_str(req, "text");
		if (!text)
			return resp_err(wc, id, "text required");
		return send_unicode_text(input, text) ? resp_ok(wc, id)
		                                      : resp_err(wc, id, "send unicode failed");
	}
	if (strcmp(op, "clipboard_get") == 0)
		return clip_get(wc, id, (UINT32)json_int(req, "timeout_ms", 3000));
	if (strcmp(op, "clipboard_set") == 0)
		return clip_set(wc, id, json_str(req, "text"));
	if (strcmp(op, "resize") == 0)
		return handle_resize(wc, id, req);

	return resp_err(wc, id, "unknown op: %s", op);
}
