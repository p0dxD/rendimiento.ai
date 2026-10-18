package traces

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	trace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type fakeStore struct {
	mu     sync.Mutex
	traces []store.Trace
	spans  []store.Span
}

func (f *fakeStore) EnsureTracePartitions(context.Context, time.Time) error      { return nil }
func (f *fakeStore) DropTracePartitions(context.Context, time.Time) (int, error) { return 0, nil }
func (f *fakeStore) SaveTraces(_ context.Context, t []store.Trace, s []store.Span) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.traces, f.spans = append(f.traces, t...), append(f.spans, s...)
	return nil
}

func span(trace, id, parent, service string, start time.Time, ms int, failed bool) store.Span {
	return store.Span{TraceID: trace, ID: id, ParentID: parent, Service: service, Name: service + " " + id,
		StartedAt: start, DurationUS: int64(ms) * 1000, Error: failed}
}

// Failed and slow requests are kept whole, normal ones by sample, and a
// span arriving after its request was kept joins it.
func TestKeeper(t *testing.T) {
	st := &fakeStore{}
	k := &Keeper{Store: st, Log: slog.Default(), Sample: 0, Slow: time.Second, Wait: 20 * time.Second}
	now := time.Now()
	const failed, slow, normal = "00000000000000000000000000000001", "00000000000000000000000000000002", "00000000000000000000000000000003"
	k.Add([]store.Span{
		span(failed, "a", "", "shop/web", now, 30, false),
		span(failed, "b", "a", "shop/api", now.Add(time.Millisecond), 20, true),
		span(slow, "c", "", "shop/web", now, 1500, false),
		span(normal, "d", "", "shop/web", now, 10, false),
	})
	k.Flush(context.Background(), now.Add(time.Second)) // too soon: nothing decided
	if len(st.traces) != 0 {
		t.Fatalf("decided before the wait: %+v", st.traces)
	}
	k.Flush(context.Background(), now.Add(21*time.Second))
	got := map[string]store.Trace{}
	for _, tr := range st.traces {
		got[tr.ID] = tr
	}
	if tr := got[failed]; tr.Reason != "error" || !tr.Error || tr.Service != "shop/web" || tr.Spans != 2 || len(tr.Services) != 2 {
		t.Errorf("failed request: %+v", tr)
	}
	if tr := got[slow]; tr.Reason != "slow" || tr.DurationUS != 1500000 {
		t.Errorf("slow request: %+v", tr)
	}
	if _, ok := got[normal]; ok || len(st.spans) != 3 {
		t.Errorf("normal request kept with sample 0, or spans %d", len(st.spans))
	}

	k.Add([]store.Span{span(failed, "e", "b", "shop/db", now.Add(2*time.Millisecond), 5, false), span(normal, "f", "d", "shop/api", now, 1, false)})
	k.Flush(context.Background(), now.Add(45*time.Second))
	if len(st.traces) != 2 || len(st.spans) != 4 || st.spans[3].ID != "e" {
		t.Errorf("late spans: %d traces, spans %+v", len(st.traces), st.spans)
	}
}

func TestSampled(t *testing.T) {
	kept := 0
	for i := range 10000 {
		id := make([]byte, 16)
		id[15], id[14], id[13] = byte(i), byte(i>>8), byte(i*7)
		if sampled(hexOf(id), 0.1) {
			kept++
		}
	}
	if kept < 700 || kept > 1300 {
		t.Errorf("kept %d of 10000 at 0.1", kept)
	}
	if !sampled("ab", 1) || sampled("00000000000000000000000000000003", 0) {
		t.Error("the bounds")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

// The Collector's export (protobuf, gzip) becomes spans with the resource's
// service name, kinds, status and attributes.
func TestReceiver(t *testing.T) {
	k := &Keeper{Store: &fakeStore{}, Log: slog.Default()}
	srv := httptest.NewServer(k.Handler())
	defer srv.Close()
	start := uint64(time.Now().UnixNano())
	req := &coltrace.ExportTraceServiceRequest{ResourceSpans: []*trace.ResourceSpans{{
		Resource: &resource.Resource{Attributes: []*common.KeyValue{{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "shop/web"}}}}},
		ScopeSpans: []*trace.ScopeSpans{{Spans: []*trace.Span{{
			TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{2}, 8), Name: "GET /cart",
			Kind: trace.Span_SPAN_KIND_SERVER, StartTimeUnixNano: start, EndTimeUnixNano: start + 2_500_000,
			Status:     &trace.Status{Code: trace.Status_STATUS_CODE_ERROR, Message: "boom"},
			Attributes: []*common.KeyValue{{Key: "http.status_code", Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: 500}}}},
		}}}},
	}}}
	b, _ := proto.Marshal(req)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(b)
	_ = w.Close()
	hreq, _ := http.NewRequest("POST", srv.URL+"/v1/traces", &gz)
	hreq.Header.Set("Content-Type", "application/x-protobuf")
	hreq.Header.Set("Content-Encoding", "gzip")
	res, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	p := k.pending["01010101010101010101010101010101"]
	if p == nil || len(p.spans) != 1 {
		t.Fatalf("pending = %+v", k.pending)
	}
	sp := p.spans[0]
	if sp.Service != "shop/web" || sp.Kind != "server" || !sp.Error || sp.StatusMessage != "boom" || sp.DurationUS != 2500 || sp.Attributes["http.status_code"] != int64(500) {
		t.Errorf("span = %+v", sp)
	}
}
