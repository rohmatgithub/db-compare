package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"dbcompare/internal/runner"
)

// sseStream writes Server-Sent Events. Writes are serialized because run
// workers emit events concurrently.
type sseStream struct {
	mu       sync.Mutex
	w        http.ResponseWriter
	rc       *http.ResponseController
	sawDone  bool
	lastSend time.Time
}

func startSSE(w http.ResponseWriter) *sseStream {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	// no-transform stops compressing proxies from buffering the stream;
	// X-Accel-Buffering disables buffering in nginx.
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &sseStream{w: w, rc: http.NewResponseController(w)}
	_ = s.rc.Flush()
	return s
}

func (s *sseStream) send(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(`{"error":"cannot encode event"}`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if event == "done" {
		s.sawDone = true
	}
	// Write errors mean the client went away; the request context is then
	// cancelled and the run stops on its own.
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload); err == nil {
		_ = s.rc.Flush()
		s.lastSend = time.Now()
	}
}

func (s *sseStream) emit(e runner.Event) {
	s.send(e.Name, e.Data)
}

func (s *sseStream) done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawDone
}

// keepAlive sends a heartbeat whenever the stream has been idle for the
// interval, so proxies do not close it during long queries.
func (s *sseStream) keepAlive(ctx context.Context, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(interval / 2)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.mu.Lock()
				idle := now.Sub(s.lastSend) >= interval
				s.mu.Unlock()
				if idle {
					s.send("heartbeat", map[string]string{"time": now.UTC().Format(time.RFC3339)})
				}
			}
		}
	}()
	return cancel
}
