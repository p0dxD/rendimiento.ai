package traces

import (
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

// Limits on what one span keeps: long values are cut, extra attributes and
// events dropped.
const (
	maxBody       = 32 << 20
	maxAttributes = 64
	maxValue      = 2048
	maxEvents     = 32
)

// Handler serves OTLP/HTTP (protobuf, optionally gzip) on POST /v1/traces,
// as the Collector's otlphttp exporter sends it, and /healthz.
func (k *Keeper) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("POST /v1/traces", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-protobuf") {
			http.Error(w, "only application/x-protobuf is accepted", http.StatusUnsupportedMediaType)
			return
		}
		var body io.Reader = http.MaxBytesReader(w, r.Body, maxBody)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer gz.Close()
			body = io.LimitReader(gz, maxBody)
		}
		b, err := io.ReadAll(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req coltrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(b, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		k.Add(Spans(&req))
		out, _ := proto.Marshal(&coltrace.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(out)
	})
	return mux
}

// Spans turns an OTLP export into the store's spans; the service is the
// resource's service.name (app/service for rendimiento's apps).
func Spans(req *coltrace.ExportTraceServiceRequest) []store.Span {
	var out []store.Span
	for _, rs := range req.GetResourceSpans() {
		service := "unknown"
		for _, kv := range rs.GetResource().GetAttributes() {
			if kv.GetKey() == "service.name" {
				service = fmt.Sprint(value(kv.GetValue()))
			}
		}
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				start := time.Unix(0, int64(sp.GetStartTimeUnixNano()))
				dur := int64(sp.GetEndTimeUnixNano()-sp.GetStartTimeUnixNano()) / 1000
				if sp.GetEndTimeUnixNano() < sp.GetStartTimeUnixNano() {
					dur = 0
				}
				s := store.Span{
					TraceID: hex.EncodeToString(sp.GetTraceId()), ID: hex.EncodeToString(sp.GetSpanId()),
					ParentID: hex.EncodeToString(sp.GetParentSpanId()), Service: service, Name: sp.GetName(),
					Kind: kind(sp.GetKind()), StartedAt: start, DurationUS: dur,
					Error:         sp.GetStatus().GetCode() == trace.Status_STATUS_CODE_ERROR,
					StatusMessage: cut(sp.GetStatus().GetMessage()),
					Attributes:    attributes(sp.GetAttributes()),
				}
				for i, ev := range sp.GetEvents() {
					if i == maxEvents {
						break
					}
					s.Events = append(s.Events, store.SpanEvent{
						Name: ev.GetName(), At: time.Unix(0, int64(ev.GetTimeUnixNano())), Attributes: attributes(ev.GetAttributes()),
					})
				}
				out = append(out, s)
			}
		}
	}
	return out
}

func kind(k trace.Span_SpanKind) string {
	switch k {
	case trace.Span_SPAN_KIND_SERVER:
		return "server"
	case trace.Span_SPAN_KIND_CLIENT:
		return "client"
	case trace.Span_SPAN_KIND_PRODUCER:
		return "producer"
	case trace.Span_SPAN_KIND_CONSUMER:
		return "consumer"
	}
	return "internal"
}

func attributes(kvs []*common.KeyValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]any, min(len(kvs), maxAttributes))
	for i, kv := range kvs {
		if i == maxAttributes {
			break
		}
		out[kv.GetKey()] = value(kv.GetValue())
	}
	return out
}

func value(v *common.AnyValue) any {
	switch x := v.GetValue().(type) {
	case *common.AnyValue_StringValue:
		return cut(x.StringValue)
	case *common.AnyValue_BoolValue:
		return x.BoolValue
	case *common.AnyValue_IntValue:
		return x.IntValue
	case *common.AnyValue_DoubleValue:
		return x.DoubleValue
	case *common.AnyValue_ArrayValue:
		var out []any
		for _, e := range x.ArrayValue.GetValues() {
			out = append(out, value(e))
		}
		return out
	case *common.AnyValue_KvlistValue:
		return attributes(x.KvlistValue.GetValues())
	case *common.AnyValue_BytesValue:
		return hex.EncodeToString(x.BytesValue)
	}
	return nil
}

func cut(s string) string {
	if len(s) > maxValue {
		return s[:maxValue] + "…"
	}
	return s
}
