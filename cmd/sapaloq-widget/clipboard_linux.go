//go:build linux

package main

/*
#cgo pkg-config: gtk+-3.0 gdk-pixbuf-2.0
#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <glib.h>
#include <stdlib.h>
#include <string.h>

// Read the clipboard image (screenshot, copied image) as PNG bytes.
static unsigned char *sapaloq_clipboard_png(size_t *out_len) {
	GtkClipboard *cb = gtk_clipboard_get(GDK_SELECTION_CLIPBOARD);
	if (!cb) {
		return NULL;
	}
	GdkPixbuf *pixbuf = gtk_clipboard_wait_for_image(cb);
	if (!pixbuf) {
		return NULL;
	}
	gchar *buffer = NULL;
	gsize bufsize = 0;
	GError *err = NULL;
	if (!gdk_pixbuf_save_to_buffer(pixbuf, &buffer, &bufsize, "png", &err, NULL)) {
		if (err) {
			g_error_free(err);
		}
		g_object_unref(pixbuf);
		return NULL;
	}
	g_object_unref(pixbuf);
	unsigned char *copy = (unsigned char *)malloc(bufsize);
	if (!copy) {
		g_free(buffer);
		return NULL;
	}
	memcpy(copy, buffer, bufsize);
	g_free(buffer);
	*out_len = bufsize;
	return copy;
}
*/
import "C"

import (
	"encoding/base64"
	"fmt"
	"unsafe"
)

func clipboardGetImageLinux() (*clipboardImage, error) {
	var n C.size_t
	ptr := C.sapaloq_clipboard_png(&n)
	if ptr == nil || n == 0 {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(ptr))
	raw := C.GoBytes(unsafe.Pointer(ptr), C.int(n))
	if len(raw) == 0 {
		return nil, nil
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	return &clipboardImage{
		DataURI: fmt.Sprintf("data:image/png;base64,%s", b64),
		MIME:    "image/png",
		Size:    len(raw),
	}, nil
}
