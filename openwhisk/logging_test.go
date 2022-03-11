package openwhisk

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDatadogJSON(t *testing.T) {
	by, err := FormatDatadog(LogLine{
		Message:      `{"message": "foo", "attribute": "bar"}`,
		Time:         time.Unix(0, 0),
		Stream:       "stdout",
		ActionName:   "testaction",
		ActivationId: "testid",
	})
	assert.NoError(t, err, "failed to format log line")

	var got map[string]interface{}
	assert.NoError(t, json.Unmarshal(by, &got), "failed to unmarshal log line")

	want := map[string]interface{}{
		"message":   "foo", // This and 'attribute' are flattened into the structure.
		"attribute": "bar",
		"date":      float64(0), // Generic parsing transforms numbers into float64.
		"ddtags":    "host:testaction,activationid:testid",
		"ddsource":  "testaction",
		"service":   "testaction",
	}

	assert.Equal(t, want, got)
}

func TestHttpLoggerSend(t *testing.T) {
	req := make(chan *http.Request, 1)
	format := FormatLogtail
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
		ActionName:   "sample/test",
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
		format: FormatLogtail,
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
		format: FormatLogtail,
	}

	assert.Error(t, logger.Send(LogLine{}))
}

type testTransport func(*http.Request) (*http.Response, error)

func (t testTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t(r)
}
