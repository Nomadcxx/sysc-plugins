package calendar

/*
#cgo pkg-config: libecal-2.0 json-glib-1.0
#include <libecal/libecal.h>
#include <json-glib/json-glib.h>
#include <time.h>

#define SYSC_MAX_INSTANCES_PER_EVENT 4096

typedef struct {
	GPtrArray *strings;   // gchar* serialized VEVENTs
	GArray *skip_times;   // time_t occurrence starts replaced by detached overrides
	ICalTimezone *utc;
	guint count;
	gboolean truncated;
} SyscExpandCtx;

static gboolean sysc_time_in_skip_list(SyscExpandCtx *ctx, ICalTime *value) {
	time_t stamp = i_cal_time_as_timet_with_zone(value, ctx->utc);
	for (guint i = 0; i < ctx->skip_times->len; i++) {
		if (g_array_index(ctx->skip_times, time_t, i) == stamp)
			return TRUE;
	}
	return FALSE;
}

static gboolean sysc_has_property(ICalComponent *component, ICalPropertyKind kind) {
	ICalProperty *prop = i_cal_component_get_first_property(component, kind);
	if (!prop)
		return FALSE;
	g_object_unref(prop);
	return TRUE;
}

static gboolean sysc_push_instance(SyscExpandCtx *ctx, ICalComponent *source,
                                   ICalTime *start, ICalTime *end, gboolean annotate) {
	if (ctx->count >= SYSC_MAX_INSTANCES_PER_EVENT) {
		ctx->truncated = TRUE;
		return FALSE;
	}
	ICalComponent *clone = i_cal_component_clone(source);
	if (start)
		i_cal_component_set_dtstart(clone, start);
	if (end) {
		ICalTime *source_end = i_cal_component_get_dtend(source);
		if (source_end) {
			i_cal_component_set_dtend(clone, end);
			g_object_unref(source_end);
		}
	}
	if (annotate) {
		// Occurrences of one master share UID + empty RECURRENCE-ID, which
		// would collapse to a single Event.ID downstream. Annotate each
		// occurrence with its start as the recurrence id; detached overrides
		// already carry this value, so override wins on ID collision.
		// get_recurrenceid returns a non-null empty time even when the
		// property is absent; check the property itself.
		if (!sysc_has_property(clone, I_CAL_RECURRENCEID_PROPERTY)) {
			ICalTime *rid = i_cal_time_clone(start);
			i_cal_component_set_recurrenceid(clone, rid);
			g_object_unref(rid); // The setter copies; the clone was ours.
		}
	}
	g_ptr_array_add(ctx->strings, i_cal_component_as_ical_string(clone));
	ctx->count++;
	g_object_unref(clone);
	return TRUE;
}

static gboolean sysc_recur_instance_cb(ICalComponent *icomp, ICalTime *instance_start,
                                       ICalTime *instance_end, gpointer user_data,
                                       GCancellable *cancellable, GError **error) {
	SyscExpandCtx *ctx = user_data;
	if (sysc_time_in_skip_list(ctx, instance_start))
		return TRUE;
	return sysc_push_instance(ctx, icomp, instance_start, instance_end, TRUE);
}

static ICalTimezone *sysc_resolve_tz(const gchar *tzid, gpointer user_data,
                                     GCancellable *cancellable, GError **error) {
	ICalTimezone *zone = i_cal_timezone_get_builtin_timezone(tzid);
	if (!zone)
		zone = i_cal_timezone_get_builtin_timezone_from_tzid(tzid);
	return zone;
}

static time_t sysc_time_stamp(ICalTime *value, ICalTimezone *utc) {
	return value ? i_cal_time_as_timet_with_zone(value, utc) : 0;
}

// A literal VEVENT stays when its [start, end) overlaps the query range, the
// same test the EDS occur-in-time-range? sexp applied before expansion. A
// missing DTEND is a zero-duration event spanning only its start.
static gboolean sysc_vevent_overlaps_range(ICalComponent *vevent, ICalTimezone *utc,
                                           gint64 start, gint64 end) {
	ICalTime *dtstart = i_cal_component_get_dtstart(vevent);
	ICalTime *dtend = i_cal_component_get_dtend(vevent);
	time_t starts = sysc_time_stamp(dtstart, utc);
	time_t ends = dtend ? sysc_time_stamp(dtend, utc) : starts;
	if (dtstart)
		g_object_unref(dtstart);
	if (dtend)
		g_object_unref(dtend);
	return starts < (time_t) end && ends >= (time_t) start;
}

static void sysc_expand_vevent(ICalComponent *vevent, GPtrArray *vevents,
                               gint64 start, gint64 end, SyscExpandCtx *ctx) {
	gboolean detached = sysc_has_property(vevent, I_CAL_RECURRENCEID_PROPERTY);
	gboolean recurring = sysc_has_property(vevent, I_CAL_RRULE_PROPERTY) ||
	                     sysc_has_property(vevent, I_CAL_RDATE_PROPERTY);
	if (detached || !recurring) {
		// Detached override or plain event: a literal, not a rule to expand.
		if (sysc_vevent_overlaps_range(vevent, ctx->utc, start, end))
			sysc_push_instance(ctx, vevent, NULL, NULL, FALSE);
		return;
	}
	// Collect occurrence starts that a detached override moved, so the master
	// expansion does not re-emit them at the original time.
	const gchar *uid = i_cal_component_get_uid(vevent);
	ctx->skip_times = g_array_new(FALSE, FALSE, sizeof(time_t));
	for (guint i = 0; i < vevents->len; i++) {
		ICalComponent *sibling = g_ptr_array_index(vevents, i);
		if (sibling == vevent)
			continue;
		if (!sysc_has_property(sibling, I_CAL_RECURRENCEID_PROPERTY))
			continue;
		gboolean same = g_strcmp0(i_cal_component_get_uid(sibling), uid) == 0;
		if (!same)
			continue;
		ICalTime *rid = i_cal_component_get_recurrenceid(sibling);
		if (rid) {
			g_array_append_val(ctx->skip_times, (time_t) { i_cal_time_as_timet_with_zone(rid, ctx->utc) });
			g_object_unref(rid);
		}
	}

	ICalTime *range_start = i_cal_time_new_from_timet_with_zone((time_t) start, 0, ctx->utc);
	ICalTime *range_end = i_cal_time_new_from_timet_with_zone((time_t) end, 0, ctx->utc);
	e_cal_recur_generate_instances_sync(vevent, range_start, range_end,
	                                    sysc_recur_instance_cb, ctx,
	                                    sysc_resolve_tz, NULL, ctx->utc, NULL, NULL);
	g_object_unref(range_start);
	g_object_unref(range_end);
	g_array_free(ctx->skip_times, TRUE);
	ctx->skip_times = NULL;
}

// Expands recurring VEVENTs in an iCalendar object into one serialized VEVENT
// per occurrence inside [start, end). JSON: {"instances": [...], "truncated": bool}.
static gchar *sysc_expand_instances_json(const gchar *ical_text, gint64 start, gint64 end) {
	ICalComponent *root = i_cal_component_new_from_string(ical_text);
	if (!root)
		return NULL;
	GPtrArray *vevents = g_ptr_array_new_with_free_func(g_object_unref);
	if (i_cal_component_isa(root) == I_CAL_VEVENT_COMPONENT) {
		g_ptr_array_add(vevents, g_object_ref(root));
	} else {
		// get_first/get_next return full refs; the array's free func owns them.
		for (ICalComponent *sub = i_cal_component_get_first_component(root, I_CAL_VEVENT_COMPONENT);
		     sub; sub = i_cal_component_get_next_component(root, I_CAL_VEVENT_COMPONENT)) {
			g_ptr_array_add(vevents, sub);
		}
	}
	SyscExpandCtx ctx = {
		.strings = g_ptr_array_new_with_free_func(g_free),
		.utc = i_cal_timezone_get_utc_timezone(),
	};
	for (guint i = 0; i < vevents->len; i++) {
		sysc_expand_vevent(g_ptr_array_index(vevents, i), vevents, start, end, &ctx);
	}
	JsonBuilder *builder = json_builder_new();
	json_builder_begin_object(builder);
	json_builder_set_member_name(builder, "instances");
	json_builder_begin_array(builder);
	for (guint i = 0; i < ctx.strings->len; i++)
		json_builder_add_string_value(builder, g_ptr_array_index(ctx.strings, i));
	json_builder_end_array(builder);
	json_builder_set_member_name(builder, "truncated");
	json_builder_add_boolean_value(builder, ctx.truncated);
	json_builder_end_object(builder);
	JsonGenerator *generator = json_generator_new();
	JsonNode *json_root = json_builder_get_root(builder);
	json_generator_set_root(generator, json_root);
	gchar *result = json_generator_to_data(generator, NULL);
	json_node_unref(json_root);
	g_object_unref(generator);
	g_object_unref(builder);
	g_ptr_array_unref(ctx.strings);
	g_ptr_array_unref(vevents);
	g_object_unref(root);
	return result;
}
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// expandInstances turns one iCalendar object into one serialized VEVENT per
// occurrence inside [start, end): recurrence rules are evaluated, detached
// overrides replace their original slot, and plain events pass through when
// they start inside the range. Reports truncation if a single object expands
// past the per-event occurrence cap.
func expandInstances(ical string, start, end int64) ([]string, bool, error) {
	cInput := C.CString(ical)
	defer C.free(unsafe.Pointer(cInput))
	data := C.sysc_expand_instances_json(cInput, C.gint64(start), C.gint64(end))
	if data == nil {
		return nil, false, fmt.Errorf("calendar: failed to expand iCalendar object")
	}
	defer C.g_free(C.gpointer(unsafe.Pointer(data)))
	var wire struct {
		Instances []string `json:"instances"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(C.GoString(data)), &wire); err != nil {
		return nil, false, fmt.Errorf("calendar: decode expanded instances: %w", err)
	}
	return wire.Instances, wire.Truncated, nil
}
