#include "worker.h"

#include <errno.h>
#include <poll.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#include <freerdp/log.h>

#define MAX_FRAME (64u * 1024u * 1024u)

BOOL ipc_open(wContext* wc, const char* path)
{
	struct sockaddr_un addr = { 0 };
	if (strlen(path) >= sizeof(addr.sun_path))
	{
		(void)fprintf(stderr, "ipc path too long\n");
		return FALSE;
	}
	int fd = socket(AF_UNIX, SOCK_STREAM, 0);
	if (fd < 0)
		return FALSE;
	addr.sun_family = AF_UNIX;
	strncpy(addr.sun_path, path, sizeof(addr.sun_path) - 1);
	if (connect(fd, (struct sockaddr*)&addr, sizeof(addr)) != 0)
	{
		(void)fprintf(stderr, "connect %s: %s\n", path, strerror(errno));
		close(fd);
		return FALSE;
	}
#ifdef SO_NOSIGPIPE
	int one = 1;
	(void)setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, sizeof(one));
#endif
	wc->ipc_fd = fd;
	wc->ipc_event = CreateFileDescriptorEvent(NULL, FALSE, FALSE, fd, WINPR_FD_READ);
	return wc->ipc_event != NULL;
}

static BOOL write_all(int fd, const void* data, size_t len)
{
	const BYTE* p = data;
	while (len > 0)
	{
#ifdef MSG_NOSIGNAL
		ssize_t n = send(fd, p, len, MSG_NOSIGNAL);
#else
		ssize_t n = send(fd, p, len, 0);
#endif
		if (n < 0)
		{
			if (errno == EINTR)
				continue;
			if (errno == EAGAIN || errno == EWOULDBLOCK)
			{
				struct pollfd pfd = { .fd = fd, .events = POLLOUT };
				(void)poll(&pfd, 1, 1000);
				continue;
			}
			return FALSE;
		}
		p += n;
		len -= (size_t)n;
	}
	return TRUE;
}

static void put_be32(BYTE* p, UINT32 v)
{
	p[0] = (BYTE)(v >> 24);
	p[1] = (BYTE)(v >> 16);
	p[2] = (BYTE)(v >> 8);
	p[3] = (BYTE)v;
}

static UINT32 get_be32(const BYTE* p)
{
	return ((UINT32)p[0] << 24) | ((UINT32)p[1] << 16) | ((UINT32)p[2] << 8) | p[3];
}

BOOL ipc_send_json(wContext* wc, WINPR_JSON* msg)
{
	if (!msg)
		return FALSE;
	char* s = WINPR_JSON_PrintUnformatted(msg);
	WINPR_JSON_Delete(msg);
	if (!s)
		return FALSE;
	size_t len = strlen(s);
	BYTE hdr[5];
	put_be32(hdr, (UINT32)(len + 1));
	hdr[4] = 'J';
	BOOL rc = write_all(wc->ipc_fd, hdr, sizeof(hdr)) && write_all(wc->ipc_fd, s, len);
	free(s);
	return rc;
}

BOOL ipc_send_frame(wContext* wc, UINT32 width, UINT32 height, UINT32 req_id, const BYTE* data,
                    UINT32 src_stride)
{
	const size_t row = (size_t)width * 4;
	const size_t payload = 16 + row * height;
	BYTE* buf = malloc(5 + payload);
	if (!buf)
		return FALSE;
	put_be32(buf, (UINT32)(payload + 1));
	buf[4] = 'F';
	put_be32(buf + 5, width);
	put_be32(buf + 9, height);
	put_be32(buf + 13, (UINT32)row);
	put_be32(buf + 17, req_id);
	BYTE* dst = buf + 21;
	for (UINT32 y = 0; y < height; y++)
		memcpy(dst + row * y, data + (size_t)src_stride * y, row);
	BOOL rc = write_all(wc->ipc_fd, buf, 5 + payload);
	free(buf);
	return rc;
}

static BOOL fill(wContext* wc, BOOL blocking, BOOL* eof)
{
	if (wc->rcap - wc->rlen < 65536)
	{
		size_t ncap = wc->rcap ? wc->rcap * 2 : 131072;
		BYTE* n = realloc(wc->rbuf, ncap);
		if (!n)
		{
			*eof = TRUE;
			return FALSE;
		}
		wc->rbuf = n;
		wc->rcap = ncap;
	}
	if (blocking)
	{
		struct pollfd pfd = { .fd = wc->ipc_fd, .events = POLLIN };
		if (poll(&pfd, 1, -1) < 0 && errno != EINTR)
		{
			*eof = TRUE;
			return FALSE;
		}
	}
	ssize_t n = recv(wc->ipc_fd, wc->rbuf + wc->rlen, wc->rcap - wc->rlen, MSG_DONTWAIT);
	if (n == 0)
	{
		*eof = TRUE;
		return FALSE;
	}
	if (n < 0)
	{
		if (errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR)
			*eof = TRUE;
		return FALSE;
	}
	wc->rlen += (size_t)n;
	return TRUE;
}

WINPR_JSON* ipc_next_message(wContext* wc, BOOL blocking, BOOL* eof)
{
	*eof = FALSE;
	for (;;)
	{
		if (wc->rlen >= 4)
		{
			UINT32 len = get_be32(wc->rbuf);
			if (len == 0 || len > MAX_FRAME)
			{
				*eof = TRUE;
				return NULL;
			}
			if (wc->rlen >= 4 + (size_t)len)
			{
				BYTE type = wc->rbuf[4];
				WINPR_JSON* msg = NULL;
				if (type == 'J')
					msg = WINPR_JSON_ParseWithLength((const char*)wc->rbuf + 5, len - 1);
				memmove(wc->rbuf, wc->rbuf + 4 + len, wc->rlen - 4 - len);
				wc->rlen -= 4 + (size_t)len;
				if (msg)
					return msg;
				continue; /* ignore unknown/invalid frames */
			}
		}
		if (!fill(wc, blocking, eof))
			return NULL;
	}
}

WINPR_JSON* resp_new(INT64 id, BOOL ok)
{
	WINPR_JSON* r = WINPR_JSON_CreateObject();
	if (!r)
		return NULL;
	(void)WINPR_JSON_AddIntegerToObject(r, "id", id);
	(void)WINPR_JSON_AddBoolToObject(r, "ok", ok);
	return r;
}

BOOL resp_ok(wContext* wc, INT64 id)
{
	return ipc_send_json(wc, resp_new(id, TRUE));
}

BOOL resp_err(wContext* wc, INT64 id, const char* fmt, ...)
{
	char buf[512];
	va_list ap;
	va_start(ap, fmt);
	(void)vsnprintf(buf, sizeof(buf), fmt, ap);
	va_end(ap);
	WINPR_JSON* r = resp_new(id, FALSE);
	(void)WINPR_JSON_AddStringToObject(r, "error", buf);
	return ipc_send_json(wc, r);
}

BOOL send_event(wContext* wc, WINPR_JSON* ev)
{
	return ipc_send_json(wc, ev);
}

INT64 json_int(WINPR_JSON* obj, const char* key, INT64 def)
{
	WINPR_JSON* v = WINPR_JSON_GetObjectItemCaseSensitive(obj, key);
	if (!v || !WINPR_JSON_IsNumber(v))
		return def;
	return (INT64)WINPR_JSON_GetNumberValue(v);
}

BOOL json_bool(WINPR_JSON* obj, const char* key, BOOL def)
{
	WINPR_JSON* v = WINPR_JSON_GetObjectItemCaseSensitive(obj, key);
	if (!v || !WINPR_JSON_IsBool(v))
		return def;
	return WINPR_JSON_IsTrue(v);
}

const char* json_str(WINPR_JSON* obj, const char* key)
{
	WINPR_JSON* v = WINPR_JSON_GetObjectItemCaseSensitive(obj, key);
	if (!v || !WINPR_JSON_IsString(v))
		return NULL;
	return WINPR_JSON_GetStringValue(v);
}
