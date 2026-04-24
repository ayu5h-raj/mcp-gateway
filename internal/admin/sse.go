package admin

import (
	"encoding/json"
	"net/http"

	"github.com/ayu5h-raj/mcp-gateway/internal/event"
)

// serveSSE streams events from bus to the HTTP client until the client
// disconnects. On connect, the recent ring buffer is replayed first so a
// fresh client sees recent history.
func serveSSE(bus *event.Bus, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Replay recent.
	for _, e := range bus.Recent() {
		writeEvent(w, e)
	}
	flusher.Flush()

	// Subscribe.
	ch, unsub := bus.Subscribe()
	defer unsub()
	notify := r.Context().Done()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(w, e)
			flusher.Flush()
		case <-notify:
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, e event.Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
}
