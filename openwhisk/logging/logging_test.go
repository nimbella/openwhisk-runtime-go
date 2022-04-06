package logging

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var testMetadata = logDestinationAttributes{
	AppName:       "testapp",
	ComponentName: "testfunc",
}

func TestHttpLoggerSend(t *testing.T) {
	req := make(chan *http.Request, 1)
	format := formatLogtail(testMetadata)
	logger := &httpLogger{
		http: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			req <- r
			return httptest.NewRecorder().Result(), nil
		})},
		format: format,
	}

	line := LogLine{
		Message:      "Hello World",
		Time:         time.Time{},
		Stream:       "stdout",
		ActivationId: "abcdef",
	}
	assert.NoError(t, logger.Send(line), "failed to send log")

	body, err := ioutil.ReadAll((<-req).Body)
	assert.NoError(t, err, "failed to read request body")

	wantBytes, err := format(line)
	assert.NoError(t, err, "failed to marshal log line")

	assert.Equal(t, body, wantBytes)
}

func TestHttpLoggerSendError(t *testing.T) {
	logger := &httpLogger{
		http: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("an error")
		})},
		format: formatLogtail(testMetadata),
	}

	assert.Error(t, logger.Send(LogLine{}))
}

func TestHttpLoggerSendHttpError(t *testing.T) {
	logger := &httpLogger{
		http: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusUnauthorized)
			return rec.Result(), nil
		})},
		format: formatLogtail(testMetadata),
	}

	assert.Error(t, logger.Send(LogLine{}))
}

func TestBatchingHttpLoggerSend(t *testing.T) {
	bodies := make(chan string, 1)
	scheduledFlushs := make(chan func(), 1)
	format := formatLogtail(testMetadata)
	logger := &batchingHttpLogger{
		http: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			body, err := ioutil.ReadAll(r.Body)
			assert.NoError(t, err, "failed to read request body")
			bodies <- string(body)
			return httptest.NewRecorder().Result(), nil
		})},
		format:    format,
		batchType: batchTypeNewline,
		execAfter: testExecAfter(scheduledFlushs),
	}

	line := LogLine{
		Message:      "Hello World",
		Time:         time.Time{},
		Stream:       "stdout",
		ActivationId: "abcdef",
	}
	assert.NoError(t, logger.Send(line), "failed to send first log")
	assert.NoError(t, logger.Send(line), "failed to send second log")

	var buf bytes.Buffer
	wantLine, err := format(line)
	assert.NoError(t, err, "failed to marshal log line")
	buf.Write(wantLine)
	buf.WriteByte('\n')
	buf.Write(wantLine)

	// Execute the scheduled flush.
	(<-scheduledFlushs)()

	assert.Equal(t, buf.String(), <-bodies)

	// Write another log
	assert.NoError(t, logger.Send(line), "failed to send third log")
	(<-scheduledFlushs)()
	assert.Equal(t, string(wantLine), <-bodies)
}

func TestBatchingHttpLoggerArraySend(t *testing.T) {
	bodies := make(chan string, 1)
	format := formatLogtail(testMetadata)
	logger := &batchingHttpLogger{
		http: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			body, err := ioutil.ReadAll(r.Body)
			assert.NoError(t, err, "failed to read request body")
			bodies <- string(body)
			return httptest.NewRecorder().Result(), nil
		})},
		format:    format,
		batchType: batchTypeArray,
		execAfter: testExecAfter(nil),
	}

	line := LogLine{
		Message:      "Hello World",
		Time:         time.Time{},
		Stream:       "stdout",
		ActivationId: "abcdef",
	}
	assert.NoError(t, logger.Send(line), "failed to send first log")
	assert.NoError(t, logger.Send(line), "failed to send second log")

	var buf bytes.Buffer
	wantLine, err := format(line)
	assert.NoError(t, err, "failed to marshal log line")
	buf.WriteByte('[')
	buf.Write(wantLine)
	buf.WriteByte(',')
	buf.Write(wantLine)
	buf.WriteByte(']')

	// Force a flush.
	assert.NoError(t, logger.Flush(), "failed to flush")

	assert.Equal(t, buf.String(), <-bodies)
}

func TestBatchingHttpLoggerSendExceedLimit(t *testing.T) {
	bodies := make(chan string, 2)
	format := formatLogtail(testMetadata)
	logger := &batchingHttpLogger{
		http: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			body, err := ioutil.ReadAll(r.Body)
			assert.NoError(t, err, "failed to read request body")
			bodies <- string(body)
			return httptest.NewRecorder().Result(), nil
		})},
		format:         format,
		batchType:      batchTypeNewline,
		batchSizeLimit: 150, // fits just one entry
		execAfter:      testExecAfter(nil),
	}

	line := LogLine{
		Message:      "Hello World",
		Time:         time.Time{},
		Stream:       "stdout",
		ActivationId: "abcdef",
	}
	assert.NoError(t, logger.Send(line), "failed to send first log")
	assert.NoError(t, logger.Send(line), "failed to send second log")

	wantLine, err := format(line)
	assert.NoError(t, err, "failed to marshal log line")

	// First log is written without even flushing.
	assert.Equal(t, string(wantLine), <-bodies, "first log not as expected")

	// Force the second line out of the buffer.
	assert.NoError(t, logger.Flush(), "failed to flush")
	assert.Equal(t, string(wantLine), <-bodies, "second log not as expected")
}

type testTransport func(*http.Request) (*http.Response, error)

func (t testTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t(r)
}

func testExecAfter(scheduled chan func()) func(time.Duration, func()) *time.Timer {
	return func(_ time.Duration, f func()) *time.Timer {
		if scheduled != nil {
			scheduled <- f
		}
		return time.NewTimer(10 * time.Hour) // arbitrary large value for testing
	}
}
