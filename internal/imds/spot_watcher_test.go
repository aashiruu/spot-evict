package imds

import (
"context"
"net/http"
"net/http/httptest"
"sync/atomic"
"testing"
"time"
)

func TestWatcherDetectsIMDSInterruption(t *testing.T) {
var interruptActive atomic.Bool

srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
switch r.URL.Path {
case "/latest/api/token":
if r.Method != http.MethodPut {
w.WriteHeader(http.StatusMethodNotAllowed)
return
}
_, _ = w.Write([]byte("test-imdsv2-token"))
case "/latest/meta-data/spot/instance-action":
if r.Header.Get("X-aws-ec2-metadata-token") != "test-imdsv2-token" {
w.WriteHeader(http.StatusUnauthorized)
return
}
if !interruptActive.Load() {
w.WriteHeader(http.StatusNotFound)
return
}
w.Header().Set("Content-Type", "application/json")
_, _ = w.Write([]byte(`{"action":"terminate","time":"2026-11-24T14:30:00Z"}`))
default:
w.WriteHeader(http.StatusNotFound)
}
}))
defer srv.Close()

ctx, cancel := context.WithCancel(context.Background())
defer cancel()

w := NewWatcher(srv.URL, 20*time.Millisecond)
w.Start(ctx, true)

time.Sleep(60 * time.Millisecond)
if w.Triggered() {
t.Fatalf("expected watcher not to be triggered while IMDS returns 404")
}

interruptActive.Store(true)

deadline := time.Now().Add(500 * time.Millisecond)
for time.Now().Before(deadline) {
if w.Triggered() {
break
}
time.Sleep(10 * time.Millisecond)
}

if !w.Triggered() {
t.Fatalf("expected watcher to detect Spot ITN")
}
n := w.Notice()
if n == nil || n.Source != "imds-spot-itn" || n.Action != "terminate" || n.ActionTime != "2026-11-24T14:30:00Z" {
t.Fatalf("unexpected notice: %+v", n)
}
}
