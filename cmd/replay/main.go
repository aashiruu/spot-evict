package main

import (
"bufio"
"encoding/json"
"flag"
"fmt"
"html/template"
"os"
"path/filepath"
"sort"
"time"
)

type Event struct {
TS           string  `json:"ts"`
Event        string  `json:"event"`
RunID        string  `json:"run_id"`
WorkerID     string  `json:"worker_id"`
InstanceID   string  `json:"instance_id"`
FencingToken int64   `json:"fencing_token"`
Chunk        int64   `json:"chunk"`
TargetChunks int64   `json:"target_chunks"`
Detail       string  `json:"detail"`
Bytes        int64   `json:"bytes,omitempty"`
DurationMs   float64 `json:"duration_ms,omitempty"`
RelSec       float64 `json:"rel_sec"`
}

type ReplayPayload struct {
RunID        string   `json:"run_id"`
TotalSec     float64  `json:"total_sec"`
TargetChunks int64    `json:"target_chunks"`
Workers      []string `json:"workers"`
Events       []Event  `json:"events"`
}

func main() {
inPath := flag.String("in", "", "path to input JSONL event log")
outPath := flag.String("out", "replay.html", "path to output standalone HTML file")
flag.Parse()

if *inPath == "" {
fmt.Fprintln(os.Stderr, "usage: replay -in <events.jsonl> [-out replay.html]")
os.Exit(1)
}

f, err := os.Open(*inPath)
if err != nil {
fmt.Fprintf(os.Stderr, "open input: %v\n", err)
os.Exit(1)
}
defer f.Close()

var events []Event
var times []time.Time
scanner := bufio.NewScanner(f)
for scanner.Scan() {
line := scanner.Bytes()
if len(line) == 0 {
continue
}
var ev Event
if err := json.Unmarshal(line, &ev); err != nil {
fmt.Fprintf(os.Stderr, "parse jsonl: %v\n", err)
os.Exit(1)
}
t, err := time.Parse(time.RFC3339Nano, ev.TS)
if err != nil {
fmt.Fprintf(os.Stderr, "parse timestamp %q: %v\n", ev.TS, err)
os.Exit(1)
}
events = append(events, ev)
times = append(times, t)
}
if err := scanner.Err(); err != nil {
fmt.Fprintf(os.Stderr, "scan input: %v\n", err)
os.Exit(1)
}
if len(events) == 0 {
fmt.Fprintln(os.Stderr, "no events found in log")
os.Exit(1)
}

idx := make([]int, len(events))
for i := range idx {
idx[i] = i
}
sort.Slice(idx, func(i, j int) bool {
return times[idx[i]].Before(times[idx[j]])
})

sorted := make([]Event, len(events))
t0 := times[idx[0]]
workerSeen := map[string]bool{}
var workers []string
var targetChunks int64
for i, orig := range idx {
ev := events[orig]
ev.RelSec = times[orig].Sub(t0).Seconds()
sorted[i] = ev
if ev.InstanceID != "" && !workerSeen[ev.InstanceID] {
workerSeen[ev.InstanceID] = true
workers = append(workers, ev.InstanceID)
}
if ev.TargetChunks > targetChunks {
targetChunks = ev.TargetChunks
}
}

payload := ReplayPayload{
RunID:        sorted[0].RunID,
TotalSec:     sorted[len(sorted)-1].RelSec,
TargetChunks: targetChunks,
Workers:      workers,
Events:       sorted,
}
rawJSON, err := json.Marshal(payload)
if err != nil {
fmt.Fprintf(os.Stderr, "marshal payload: %v\n", err)
os.Exit(1)
}

if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
os.Exit(1)
}
outFile, err := os.Create(*outPath)
if err != nil {
fmt.Fprintf(os.Stderr, "create output: %v\n", err)
os.Exit(1)
}
defer outFile.Close()

tmpl := template.Must(template.New("replay").Parse(htmlTemplate))
if err := tmpl.Execute(outFile, map[string]interface{}{
"RunID":       payload.RunID,
"PayloadJSON": template.JS(rawJSON),
}); err != nil {
fmt.Fprintf(os.Stderr, "render html: %v\n", err)
os.Exit(1)
}
fmt.Printf("Generated replay for run %s (%d events, %.2fs) -> %s\n", payload.RunID, len(sorted), payload.TotalSec, *outPath)
}

const htmlTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>spot-evict Timeline Replay: {{.RunID}}</title>
<style>
  body { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: #0f172a; color: #e2e8f0; margin: 24px; }
  h1 { font-size: 18px; margin: 0 0 6px 0; color: #f8fafc; }
  .sub { font-size: 13px; color: #94a3b8; margin-bottom: 16px; }
  .controls { display: flex; align-items: center; gap: 12px; margin-bottom: 14px; background: #1e293b; padding: 10px 14px; border-radius: 6px; }
  button, select { background: #334155; color: #f8fafc; border: 1px solid #475569; padding: 5px 10px; border-radius: 4px; cursor: pointer; font-family: inherit; }
  button:hover { background: #475569; }
  input[type=range] { flex: 1; }
  canvas { display: block; width: 100%; max-width: 1040px; background: #020617; border: 1px solid #334155; border-radius: 6px; }
  .legend { display: flex; flex-wrap: wrap; gap: 16px; margin-top: 12px; font-size: 12px; color: #cbd5e1; }
  .dot { display: inline-block; width: 10px; height: 10px; border-radius: 50%; margin-right: 6px; }
</style>
</head>
<body>
  <h1>spot-evict Timeline Replay: {{.RunID}}</h1>
  <div class="sub">Generated directly from structured JSONL event log (REAL AWS Spot + FIS interruption run).</div>
  <div class="controls">
    <button id="playBtn">Pause</button>
    <label>Speed:
      <select id="speedSel">
        <option value="1">1x</option>
        <option value="5">5x</option>
        <option value="15" selected>15x</option>
        <option value="30">30x</option>
      </select>
    </label>
    <input id="scrubber" type="range" min="0" max="1000" value="0" />
    <span id="timeLabel">0.0s</span>
  </div>
  <canvas id="stage" width="1040" height="400"></canvas>
  <div class="legend">
    <span><i class="dot" style="background:#38bdf8"></i>Active Worker</span>
    <span><i class="dot" style="background:#22c55e"></i>Periodic Checkpoint</span>
    <span><i class="dot" style="background:#f59e0b"></i>IMDSv2 Notice &amp; Final Flush (120s window)</span>
    <span><i class="dot" style="background:#ef4444"></i>Termination</span>
    <span><i class="dot" style="background:#a855f7"></i>Lease Acquired / Resume</span>
  </div>
<script>
const DATA = {{.PayloadJSON}};
const canvas = document.getElementById('stage');
const ctx = canvas.getContext('2d');
const playBtn = document.getElementById('playBtn');
const speedSel = document.getElementById('speedSel');
const scrubber = document.getElementById('scrubber');
const timeLabel = document.getElementById('timeLabel');

let playing = true;
let simTime = 0;
let lastFrame = performance.now();

playBtn.onclick = () => {
  playing = !playing;
  playBtn.textContent = playing ? 'Pause' : 'Play';
  lastFrame = performance.now();
};
scrubber.oninput = () => {
  simTime = (Number(scrubber.value) / 1000) * DATA.total_sec;
};

function draw() {
  const padL = 195, padR = 30, padT = 40;
  const plotW = canvas.width - padL - padR;
  const total = Math.max(DATA.total_sec, 1);
  const toX = (s) => padL + (s / total) * plotW;

  ctx.clearRect(0, 0, canvas.width, canvas.height);

  // Grid ticks
  ctx.strokeStyle = '#1e293b';
  ctx.fillStyle = '#64748b';
  ctx.font = '11px monospace';
  for (let s = 0; s <= total; s += 30) {
    const x = toX(s);
    ctx.beginPath(); ctx.moveTo(x, padT - 10); ctx.lineTo(x, 260); ctx.stroke();
    ctx.fillText(s + 's', x - 10, padT - 16);
  }

  // Worker lanes
  const laneH = 74;
  DATA.workers.forEach((wid, idx) => {
    const y = padT + idx * laneH;
    ctx.fillStyle = '#0f172a';
    ctx.fillRect(padL, y, plotW, 48);
    ctx.strokeStyle = '#334155';
    ctx.strokeRect(padL, y, plotW, 48);
    ctx.fillStyle = '#e2e8f0';
    ctx.font = '12px monospace';
    ctx.fillText('Worker ' + (idx + 1), 16, y + 20);
    ctx.fillStyle = '#94a3b8';
    ctx.font = '11px monospace';
    ctx.fillText(wid, 16, y + 36);

    const wEvents = DATA.events.filter(e => e.instance_id === wid);
    const startEv = wEvents.find(e => e.event === 'worker_start');
    const termEv = wEvents.find(e => e.event === 'termination');
    const endSec = termEv ? termEv.rel_sec : total;
    if (startEv && simTime >= startEv.rel_sec) {
      const activeEnd = Math.min(simTime, endSec);
      ctx.fillStyle = 'rgba(56, 189, 248, 0.25)';
      ctx.fillRect(toX(startEv.rel_sec), y + 6, Math.max(2, toX(activeEnd) - toX(startEv.rel_sec)), 36);
    }

    // Notice window shading (120s IMDSv2 warning)
    const noticeEv = wEvents.find(e => e.event === 'notice_received');
    if (noticeEv && simTime >= noticeEv.rel_sec) {
      const winEnd = Math.min(total, noticeEv.rel_sec + 120);
      ctx.fillStyle = 'rgba(245, 158, 11, 0.14)';
      ctx.fillRect(toX(noticeEv.rel_sec), y + 2, toX(winEnd) - toX(noticeEv.rel_sec), 44);
      const rem = Math.max(0, 120 - (simTime - noticeEv.rel_sec));
      if (simTime <= winEnd) {
        ctx.fillStyle = '#fbbf24';
        ctx.fillText('ITN 120s window: ' + rem.toFixed(1) + 's left', toX(noticeEv.rel_sec) + 4, y + 62);
      }
    }

    wEvents.forEach(ev => {
      if (ev.rel_sec > simTime) return;
      const x = toX(ev.rel_sec);
      if (ev.event === 'checkpoint_written') {
        const isEmergency = ev.detail && ev.detail.startsWith('emergency:');
        ctx.fillStyle = isEmergency ? '#f59e0b' : '#22c55e';
        ctx.beginPath(); ctx.arc(x, y + 24, 6, 0, Math.PI * 2); ctx.fill();
      } else if (ev.event === 'lease_acquired' || ev.event === 'checkpoint_loaded') {
        ctx.fillStyle = '#a855f7';
        ctx.fillRect(x - 3, y + 8, 6, 32);
      } else if (ev.event === 'termination') {
        ctx.fillStyle = '#ef4444';
        ctx.fillRect(x - 2, y + 4, 4, 40);
      }
    });
  });

  // Job progress bar
  let currentChunk = 0;
  let activeToken = 0;
  let statusLine = 'Waiting for worker...';
  DATA.events.forEach(ev => {
    if (ev.rel_sec <= simTime) {
      if (ev.chunk > 0) currentChunk = ev.chunk;
      if (ev.fencing_token > 0) activeToken = ev.fencing_token;
      statusLine = '[' + ev.rel_sec.toFixed(2) + 's] ' + ev.event + ' (' + ev.instance_id + ', token=' + ev.fencing_token + ', chunk=' + ev.chunk + ')';
    }
  });
  const pct = Math.min(1, currentChunk / Math.max(DATA.target_chunks, 1));
  const barY = 290;
  ctx.fillStyle = '#1e293b';
  ctx.fillRect(padL, barY, plotW, 24);
  ctx.fillStyle = '#22c55e';
  ctx.fillRect(padL, barY, plotW * pct, 24);
  ctx.strokeStyle = '#475569';
  ctx.strokeRect(padL, barY, plotW, 24);
  ctx.fillStyle = '#f8fafc';
  ctx.font = '12px monospace';
  ctx.fillText('Job Progress', 16, barY + 16);
  ctx.fillText(currentChunk.toLocaleString() + ' / ' + DATA.target_chunks.toLocaleString() + ' chunks (' + (pct * 100).toFixed(1) + '%) | fencing_token=' + activeToken, padL + 10, barY + 16);

  ctx.fillStyle = '#94a3b8';
  ctx.fillText(statusLine, 16, 352);

  // Playhead
  const px = toX(simTime);
  ctx.strokeStyle = '#f8fafc';
  ctx.beginPath(); ctx.moveTo(px, padT - 10); ctx.lineTo(px, barY + 28); ctx.stroke();

  timeLabel.textContent = simTime.toFixed(1) + 's / ' + total.toFixed(1) + 's';
  scrubber.value = Math.round((simTime / total) * 1000);
}

function tick(now) {
  const dt = (now - lastFrame) / 1000;
  lastFrame = now;
  if (playing) {
    simTime += dt * Number(speedSel.value);
    if (simTime >= DATA.total_sec) {
      simTime = DATA.total_sec;
      playing = false;
      playBtn.textContent = 'Replay';
    }
  }
  draw();
  requestAnimationFrame(tick);
}
requestAnimationFrame(tick);
</script>
</body>
</html>
`
