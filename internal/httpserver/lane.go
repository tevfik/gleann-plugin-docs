package httpserver

import (
	"context"
	"math"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Work classes (P7). Heavy work is memory- and CPU-hungry (OCR, VLM,
// LibreOffice, rasterised PDFs and images); light work is the streaming
// Office, HTML and text extractors. Each class has its own run slots, queue
// and Retry-After estimate, so a backlog of scans never starves a docx.
const (
	classHeavy = "heavy"
	classLight = "light"
)

// heavyExts are the formats that are always heavy: PDF (text layer, page
// rasterising, OCR/VLM), images (OCR/VLM), the formats that go through
// LibreOffice or anydoc, and .xls (its native reader can fall back to
// LibreOffice).
var heavyExts = map[string]bool{
	".pdf": true,
	".png": true, ".jpg": true, ".jpeg": true, ".tif": true, ".tiff": true, ".bmp": true, ".webp": true,
	".doc": true, ".xls": true, ".ppt": true, ".odt": true, ".ods": true, ".odp": true, ".rtf": true,
}

// pictureExts are light unless OCR or the VLM is enabled: they run it on every
// embedded picture then.
var pictureExts = map[string]bool{".docx": true, ".pptx": true, ".epub": true}

// imageTexter is implemented by engines that know whether OCR or the VLM is
// configured.
type imageTexter interface{ ImageTextAvailable() bool }

// classOf classifies an upload from its lower-case extension and the server's
// OCR/VLM configuration, before any slot is taken. There are no per-request
// options.
func classOf(ext string, imageText bool) string {
	if heavyExts[ext] || (imageText && pictureExts[ext]) {
		return classHeavy
	}
	return classLight
}

// classFor is classOf with the answer taken from the server's engine.
func (s *Server) classFor(ext string) string {
	it, _ := s.eng.(imageTexter)
	return classOf(ext, it != nil && it.ImageTextAvailable())
}

// lane is one work class: its run slots, its queue and its duration samples.
type lane struct {
	class      string
	maxRunning int
	queueBound int
	admitCh    chan struct{} // tickets: running + waiting conversions
	runCh      chan struct{} // running conversion slots
	queued     atomic.Int64  // conversions waiting for a run slot

	durMu     sync.Mutex
	durations []time.Duration // recent finished conversion durations (cancelled ones excluded)
}

func newLane(class string, running, queue int) *lane {
	return &lane{
		class:      class,
		maxRunning: running,
		queueBound: queue,
		admitCh:    make(chan struct{}, running+queue),
		runCh:      make(chan struct{}, running),
	}
}

// tryTicket takes a ticket without waiting; false when running + waiting are at the cap.
func (l *lane) tryTicket() bool {
	select {
	case l.admitCh <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *lane) releaseTicket() { <-l.admitCh }

// acquire waits for a run slot of this lane. It returns errDraining when
// shutdown has started and the context's error when the client went away; on
// nil the caller owns a slot. The state is re-checked after the slot is taken
// because select picks among ready cases at random.
func (l *lane) acquire(ctx context.Context, s *Server) error {
	select {
	case l.runCh <- struct{}{}:
	default:
		l.queued.Add(1)
		select {
		case l.runCh <- struct{}{}:
			l.queued.Add(-1)
		case <-ctx.Done():
			l.queued.Add(-1)
			return ctx.Err()
		case <-s.drainCh:
			l.queued.Add(-1)
			return errDraining
		}
	}
	if s.draining() {
		<-l.runCh
		return errDraining
	}
	if err := ctx.Err(); err != nil {
		<-l.runCh
		return err
	}
	return nil
}

func (l *lane) release() { <-l.runCh }

// recordDuration keeps the last durationSamples finished conversion durations.
func (l *lane) recordDuration(d time.Duration) {
	l.durMu.Lock()
	defer l.durMu.Unlock()
	if len(l.durations) >= durationSamples {
		copy(l.durations, l.durations[1:])
		l.durations[len(l.durations)-1] = d
		return
	}
	l.durations = append(l.durations, d)
}

// retryAfterSeconds estimates how long until a request arriving now would get
// a slot of this lane: its queue position / concurrency x the recent median
// conversion duration (1 s before any sample), clamped to 1..retryAfterMax.
func (l *lane) retryAfterSeconds() int {
	l.durMu.Lock()
	ds := slices.Clone(l.durations)
	l.durMu.Unlock()
	median := time.Second
	if len(ds) > 0 {
		slices.Sort(ds)
		median = ds[len(ds)/2]
	}
	pos := float64(l.queued.Load() + 1)
	secs := int(math.Ceil(pos / float64(l.maxRunning) * median.Seconds()))
	return min(max(secs, 1), retryAfterMax)
}

func (l *lane) retryAfter() string { return strconv.Itoa(l.retryAfterSeconds()) }

// status is the lane's /health entry.
func (l *lane) status() map[string]any {
	return map[string]any{
		"max_concurrency": l.maxRunning,
		"max_queue":       l.queueBound,
		"running":         len(l.runCh),
		"queued":          l.queued.Load(),
	}
}

// lane returns the lane of a class.
func (s *Server) laneFor(class string) *lane {
	if class == classHeavy {
		return s.heavy
	}
	return s.light
}

// retryAfterAny is the Retry-After for a response written before the class is
// known (engine missing, shutdown): the longer of the two estimates.
func (s *Server) retryAfterAny() string {
	return strconv.Itoa(max(s.heavy.retryAfterSeconds(), s.light.retryAfterSeconds()))
}
