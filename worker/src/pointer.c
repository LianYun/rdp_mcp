#include "worker.h"

#include <string.h>

#include <freerdp/codec/color.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/graphics.h>

typedef struct
{
	rdpPointer pointer;
	BYTE* bgra;
} wPointer;

static void cursor_changed(wContext* wc)
{
	wc->cursor.changed = TRUE;
	(void)SetEvent(wc->wake);
}

static BOOL w_pointer_new(rdpContext* context, rdpPointer* pointer)
{
	wPointer* p = (wPointer*)pointer;
	const size_t size = (size_t)pointer->width * pointer->height * 4;
	p->bgra = NULL;
	if (size == 0)
		return TRUE;
	p->bgra = winpr_aligned_malloc(size, 16);
	if (!p->bgra)
		return FALSE;
	if (!freerdp_image_copy_from_pointer_data(p->bgra, PIXEL_FORMAT_BGRA32, pointer->width * 4, 0,
	                                          0, pointer->width, pointer->height,
	                                          pointer->xorMaskData, pointer->lengthXorMask,
	                                          pointer->andMaskData, pointer->lengthAndMask,
	                                          pointer->xorBpp, &context->gdi->palette))
	{
		winpr_aligned_free(p->bgra);
		p->bgra = NULL;
		return FALSE;
	}
	return TRUE;
}

static void w_pointer_free(rdpContext* context, rdpPointer* pointer)
{
	wContext* wc = (wContext*)context;
	wPointer* p = (wPointer*)pointer;
	if (wc->cursor.bgra == p->bgra)
		wc->cursor.bgra = NULL;
	winpr_aligned_free(p->bgra);
	p->bgra = NULL;
}

static BOOL w_pointer_set(rdpContext* context, rdpPointer* pointer)
{
	wContext* wc = (wContext*)context;
	wPointer* p = (wPointer*)pointer;
	wc->cursor.visible = TRUE;
	wc->cursor.bgra = p->bgra;
	wc->cursor.width = pointer->width;
	wc->cursor.height = pointer->height;
	wc->cursor.hot_x = pointer->xPos;
	wc->cursor.hot_y = pointer->yPos;
	wc->cursor.shape_seq++;
	cursor_changed(wc);
	return TRUE;
}

static BOOL w_pointer_set_null(rdpContext* context)
{
	wContext* wc = (wContext*)context;
	wc->cursor.visible = FALSE;
	wc->cursor.shape_seq++;
	cursor_changed(wc);
	return TRUE;
}

static BOOL w_pointer_set_default(rdpContext* context)
{
	wContext* wc = (wContext*)context;
	wc->cursor.visible = TRUE;
	wc->cursor.bgra = NULL;
	wc->cursor.width = wc->cursor.height = 0;
	wc->cursor.hot_x = wc->cursor.hot_y = 0;
	wc->cursor.shape_seq++;
	cursor_changed(wc);
	return TRUE;
}

static BOOL w_pointer_set_position(rdpContext* context, UINT32 x, UINT32 y)
{
	pointer_set_position((wContext*)context, (INT32)x, (INT32)y);
	return TRUE;
}

void pointer_set_position(wContext* wc, INT32 x, INT32 y)
{
	if (wc->cursor.x == x && wc->cursor.y == y)
		return;
	wc->cursor.x = x;
	wc->cursor.y = y;
	cursor_changed(wc);
}

BOOL pointer_register(rdpContext* context)
{
	rdpPointer pointer = { 0 };
	pointer.size = sizeof(wPointer);
	pointer.New = w_pointer_new;
	pointer.Free = w_pointer_free;
	pointer.Set = w_pointer_set;
	pointer.SetNull = w_pointer_set_null;
	pointer.SetDefault = w_pointer_set_default;
	pointer.SetPosition = w_pointer_set_position;
	graphics_register_pointer(context->graphics, &pointer);
	wContext* wc = (wContext*)context;
	wc->cursor.visible = TRUE;
	return TRUE;
}
