package imds

import (
"context"
"encoding/json"
"fmt"
"io"
"net/http"
"os"
"os/signal"
"strings"
"sync"
"sync/atomic"
"syscall"
"time"
)

const DefaultIMDSBaseURL = "http://169.254.169.254"

// InstanceAction represents the JSON payload returned by IMDS /latest/meta-data/spot/instance-action.
type InstanceAction struct {
Action string `json:"action"`
Time   string `json:"time"`
}

// InterruptionNotice captures why and when an interruption signal was received.
type InterruptionNotice struct {
Source     string `json:"source"`      // "imds-spot-itn" or "os-signal"
Action     string `json:"action"`      // e.g. "terminate", "stop", "hibernate", or "SIGTERM"
ActionTime string `json:"action_time"` // RFC3339 timestamp from IMDS or local detection time
DetectedAt string `json:"detected_at"`
}

// Watcher polls IMDSv2 for Spot Instance Interruption Notices and listens for OS termination signals.
type Watcher struct {
BaseURL      string
PollInterval time.Duration
Client       *http.Client

triggered atomic.Bool
mu        sync.RWMutex
notice    *InterruptionNotice
token     string
tokenExp  time.Time
}

// NewWatcher creates a Spot ITN + OS signal watcher. If baseURL is empty, DefaultIMDSBaseURL is used.
func NewWatcher(baseURL string, pollInterval time.Duration) *Watcher {
if baseURL == "" {
baseURL = DefaultIMDSBaseURL
}
if pollInterval <= 0 {
pollInterval = 2 * time.Second
}
return &Watcher{
BaseURL:      strings.TrimRight(baseURL, "/"),
PollInterval: pollInterval,
Client: &http.Client{
Timeout: 1500 * time.Millisecond,
},
}
}

// Triggered returns true once an interruption notice or SIGTERM/SIGINT has been observed.
func (w *Watcher) Triggered() bool {
return w.triggered.Load()
}

// Notice returns the recorded InterruptionNotice, or nil if none has occurred.
func (w *Watcher) Notice() *InterruptionNotice {
w.mu.RLock()
defer w.mu.RUnlock()
if w.notice == nil {
return nil
}
cp := *w.notice
return &cp
}

func (w *Watcher) record(n InterruptionNotice) {
w.mu.Lock()
if w.notice == nil {
w.notice = &n
}
w.mu.Unlock()
w.triggered.Store(true)
}

// Start launches background polling for IMDS Spot ITN (when enableIMDS is true) and OS signals.
func (w *Watcher) Start(ctx context.Context, enableIMDS bool) {
sigCh := make(chan os.Signal, 2)
signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

go func() {
defer signal.Stop(sigCh)
var ticker *time.Ticker
var tickCh <-chan time.Time
if enableIMDS {
ticker = time.NewTicker(w.PollInterval)
defer ticker.Stop()
tickCh = ticker.C
// Check immediately on startup in case notice was already issued before process start
w.pollOnce(ctx)
}

for {
select {
case <-ctx.Done():
return
case sig := <-sigCh:
now := time.Now().UTC().Format(time.RFC3339Nano)
w.record(InterruptionNotice{
Source:     "os-signal",
Action:     sig.String(),
ActionTime: now,
DetectedAt: now,
})
return
case <-tickCh:
w.pollOnce(ctx)
if w.Triggered() {
return
}
}
}
}()
}

func (w *Watcher) fetchToken(ctx context.Context) (string, error) {
if w.token != "" && time.Now().Before(w.tokenExp) {
return w.token, nil
}
req, err := http.NewRequestWithContext(ctx, http.MethodPut, w.BaseURL+"/latest/api/token", nil)
if err != nil {
return "", err
}
req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
resp, err := w.Client.Do(req)
if err != nil {
return "", err
}
defer resp.Body.Close()
if resp.StatusCode != http.StatusOK {
return "", fmt.Errorf("imds token status %d", resp.StatusCode)
}
raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
if err != nil {
return "", err
}
w.token = strings.TrimSpace(string(raw))
w.tokenExp = time.Now().Add(5 * time.Hour)
return w.token, nil
}

func (w *Watcher) pollOnce(ctx context.Context) {
tok, err := w.fetchToken(ctx)
if err != nil {
return
}
req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.BaseURL+"/latest/meta-data/spot/instance-action", nil)
if err != nil {
return
}
req.Header.Set("X-aws-ec2-metadata-token", tok)

resp, err := w.Client.Do(req)
if err != nil {
return
}
defer resp.Body.Close()

// HTTP 404 is the normal state when no Spot interruption notice is active
if resp.StatusCode == http.StatusNotFound {
return
}
if resp.StatusCode == http.StatusUnauthorized {
w.token = ""
return
}
if resp.StatusCode != http.StatusOK {
return
}

raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
if err != nil {
return
}
var action InstanceAction
if err := json.Unmarshal(raw, &action); err != nil {
return
}
if action.Action != "" {
w.record(InterruptionNotice{
Source:     "imds-spot-itn",
Action:     action.Action,
ActionTime: action.Time,
DetectedAt: time.Now().UTC().Format(time.RFC3339Nano),
})
}
}
