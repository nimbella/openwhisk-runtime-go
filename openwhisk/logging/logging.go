package logging

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type LogLine struct {
	Message      string
	Time         time.Time
	Stream       string
	ActivationId string
}

type RemoteLogger interface {
	// Send sends a logline to the remote service. Implementations can choose to batch
	// lines.
	Send(LogLine) error

	// Flush sends all potentially buffered log lines to the remote service.
	Flush() error
}

// httpLogger sends a logline per HTTP request. No batching is done.
type httpLogger struct {
	http    *http.Client
	url     string
	headers map[string]string
	format  func(LogLine) ([]byte, error)
}

func (l *httpLogger) Send(line LogLine) error {
	by, err := l.format(line)
	if err != nil {
		return fmt.Errorf("failed to marshal logline: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, l.url, bytes.NewBuffer(by))
	if err != nil {
		return fmt.Errorf("failed to construct HTTP request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	for key, val := range l.headers {
		req.Header.Add(key, val)
	}

	res, err := l.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		return fmt.Errorf("failed to ingest log line, code: %d", res.StatusCode)
	}
	return nil
}

func (l *httpLogger) Flush() error {
	return nil
}

// batchType defines the format in which will be batched.
type batchType int

const (
	// batchTypeArray means that the log lines are batched using JSON arrays.
	batchTypeArray batchType = iota
	// batchTypeNewline means that the log lines are delimited lines in the response.
	batchTypeNewline
)

// batchingHttpLogger batches log lines according to the given batchType.
// Batches are written out either after the given interval, if the given size limit
// would be exceeded or if they are explicitly flushed.
type batchingHttpLogger struct {
	http    *http.Client
	url     string
	headers map[string]string
	format  func(LogLine) ([]byte, error)

	batchType      batchType
	batchInterval  time.Duration
	batchSizeLimit int
	execAfter      func(time.Duration, func()) *time.Timer

	mux   sync.Mutex
	timer *time.Timer
	buf   bytes.Buffer
}

func (l *batchingHttpLogger) Send(line LogLine) error {
	l.mux.Lock()
	defer l.mux.Unlock()

	formatted, err := l.format(line)
	if err != nil {
		return fmt.Errorf("failed to marshal logline: %w", err)
	}

	// Send immediately if our batch would exceed the defined limit.
	if l.batchSizeLimit > 0 && l.buf.Len()+len(formatted) > l.batchSizeLimit {
		if err := l.sendBatch(); err != nil {
			return err
		}
	}

	if l.batchType == batchTypeArray {
		if l.buf.Len() == 0 {
			// If the buffer was still empty, open the array...
			l.buf.WriteByte('[')
		} else {
			// ...else delimit from the last element using a comma.
			l.buf.WriteByte(',')
		}
	} else {
		if l.buf.Len() > 0 {
			// Prepend all logs with a newline.
			l.buf.WriteByte('\n')
		}
	}
	l.buf.Write(formatted)

	if l.timer == nil {
		// Schedule a flush if there isn't already one scheduled.
		l.timer = l.execAfter(l.batchInterval, func() { l.Flush() })
	}

	return nil
}

func (l *batchingHttpLogger) Flush() error {
	l.mux.Lock()
	defer l.mux.Unlock()

	if l.timer != nil {
		// Cancel a potentially scheduled flush.
		l.timer.Stop()
		l.timer = nil
	}

	if l.buf.Len() > 0 {
		return l.sendBatch()
	}
	return nil
}

// sendBatch sends the batch to the remote.
// mux must be held.
func (l *batchingHttpLogger) sendBatch() error {
	defer l.buf.Reset()

	if l.batchType == batchTypeArray {
		// Close the array if that's the type of batching being used.
		l.buf.WriteByte(']')
	}

	req, err := http.NewRequest(http.MethodPost, l.url, &l.buf)
	if err != nil {
		return fmt.Errorf("failed to construct HTTP request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	for key, val := range l.headers {
		req.Header.Add(key, val)
	}

	res, err := l.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		return fmt.Errorf("failed to ingest log line, code: %d", res.StatusCode)
	}
	return nil
}
