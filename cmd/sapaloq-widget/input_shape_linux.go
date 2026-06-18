//go:build linux

package main

/*
#cgo pkg-config: gtk+-3.0
#cgo LDFLAGS: -lm
#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <glib.h>
#include <cairo.h>
#include <math.h>
#include <string.h>
#include <stdlib.h>

typedef struct {
	int w;
	int h;
	int circle;
	int attempt;
} ShapeRetryArgs;

static cairo_region_t *sapaloq_circle_region(int w, int h) {
	cairo_region_t *region = cairo_region_create();
	int cx = w / 2;
	int cy = h / 2;
	int r = (w < h ? w : h) / 2;
	for (int dy = -r; dy <= r; dy++) {
		int dx = (int)sqrt((double)(r * r - dy * dy));
		cairo_rectangle_int_t rect = { cx - dx, cy + dy, dx * 2, 1 };
		cairo_region_union_rectangle(region, &rect);
	}
	return region;
}

static gboolean sapaloq_apply_input_shape_once(const char *title_match, int w, int h, int circle) {
	GList *toplevels = gtk_window_list_toplevels();
	gboolean applied = FALSE;
	for (GList *iter = toplevels; iter != NULL; iter = iter->next) {
		GtkWindow *win = GTK_WINDOW(iter->data);
		const char *title = gtk_window_get_title(win);
		if (!title || strstr(title, title_match) == NULL) {
			continue;
		}
		GtkWidget *widget = GTK_WIDGET(win);
		if (!gtk_widget_get_realized(widget)) {
			gtk_widget_realize(widget);
		}
		cairo_region_t *region;
		if (circle) {
			region = sapaloq_circle_region(w, h);
		} else {
			cairo_rectangle_int_t rect = { .x = 0, .y = 0, .width = w, .height = h };
			region = cairo_region_create_rectangle(&rect);
		}
		gtk_widget_input_shape_combine_region(widget, region);
		cairo_region_destroy(region);
		applied = TRUE;
		break;
	}
	g_list_free(toplevels);
	return applied;
}

static gboolean sapaloq_apply_shape_retry_timeout(gpointer data) {
	ShapeRetryArgs *args = (ShapeRetryArgs *)data;
	if (sapaloq_apply_input_shape_once("sapaloq", args->w, args->h, args->circle)) {
		g_free(args);
		return G_SOURCE_REMOVE;
	}
	args->attempt++;
	if (args->attempt >= 10) {
		g_free(args);
		return G_SOURCE_REMOVE;
	}
	return G_SOURCE_CONTINUE;
}

static void sapaloq_schedule_shape(int w, int h, int circle) {
	ShapeRetryArgs *args = g_malloc(sizeof(ShapeRetryArgs));
	args->w = w;
	args->h = h;
	args->circle = circle;
	args->attempt = 0;
	g_timeout_add(50, sapaloq_apply_shape_retry_timeout, args);
}
*/
import "C"

func scheduleInputShape(collapsed bool) {
	if collapsed {
		C.sapaloq_schedule_shape(48, 48, 1)
	} else {
		C.sapaloq_schedule_shape(360, 520, 0)
	}
}
