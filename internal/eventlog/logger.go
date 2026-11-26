package eventlog

import (
"bytes"
"context"
"encoding/json"
"fmt"
"os"
"sync"
"time"

"github.com/aws/aws-sdk-go-v2/aws"
"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Event represents one structured JSONL record in the single-source-of-truth log.
type Event struct {
Timestamp    string `json:"ts"`
Event        string `json:"event"`
RunID        string `json:"run_id"`
WorkerID     string `json:"worker_id"`
InstanceID   string `json:"instance_id,omitempty"`
FencingToken int64  `json:"fencing_token"`
Chunk        int64  `json:"chunk"`
TargetChunks int64  `json:"target_chunks"`
Detail       string `json:"detail,omitempty"`
Bytes        int    `json:"bytes,omitempty"`
DurationMs   float64 `json:"duration_ms,omitempty"`
}

type Logger struct {
mu       sync.Mutex
buf      bytes.Buffer
file     *os.File
runID    string
workerID string
instID   string
target   int64
}

func New(path, runID, workerID, instanceID string, targetChunks int64) (*Logger, error) {
var f *os.File
if path != "" {
var err error
f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
if err != nil {
return nil, err
}
}
return &Logger{
file:     f,
runID:    runID,
workerID: workerID,
instID:   instanceID,
target:   targetChunks,
}, nil
}

func (l *Logger) Emit(event string, token, chunk int64, detail string, bytesCount int, durMs float64) {
if l == nil {
return
}
l.mu.Lock()
defer l.mu.Unlock()
ev := Event{
Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
Event:        event,
RunID:        l.runID,
WorkerID:     l.workerID,
InstanceID:   l.instID,
FencingToken: token,
Chunk:        chunk,
TargetChunks: l.target,
Detail:       detail,
Bytes:        bytesCount,
DurationMs:   durMs,
}
line, err := json.Marshal(ev)
if err != nil {
return
}
line = append(line, '\n')
l.buf.Write(line)
if l.file != nil {
_, _ = l.file.Write(line)
}
}

func (l *Logger) FlushToS3(ctx context.Context, client *s3.Client, bucket string) error {
if l == nil || client == nil || bucket == "" {
return nil
}
l.mu.Lock()
data := append([]byte(nil), l.buf.Bytes()...)
l.mu.Unlock()
if len(data) == 0 {
return nil
}
key := fmt.Sprintf("events/%s/%s-%d.jsonl", l.runID, l.workerID, time.Now().UTC().UnixNano())
_, err := client.PutObject(ctx, &s3.PutObjectInput{
Bucket:      aws.String(bucket),
Key:         aws.String(key),
Body:        bytes.NewReader(data),
ContentType: aws.String("application/x-ndjson"),
})
return err
}

func (l *Logger) Close() {
if l != nil && l.file != nil {
_ = l.file.Close()
}
}
