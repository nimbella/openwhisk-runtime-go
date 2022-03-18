package openwhisk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type LogLine struct {
	Message      string
	Time         time.Time
	Stream       string
	ActionName   string
	ActivationId string
}

type LogtailLogLine struct {
	Message      string `json:"message,omitempty"`
	Time         string `json:"dt,omitempty"`
	Host         string `json:"host,omitempty"`
	AppName      string `json:"appname,omitempty"`
	ActivationId string `json:"activationId,omitempty"`
}

func FormatLogtail(l LogLine) ([]byte, error) {
	return json.Marshal(LogtailLogLine{
		Message:      l.Message,
		Time:         l.Time.UTC().Format("2006-01-02 15:04:05.000000000 MST"),
		Host:         l.ActionName,
		AppName:      l.ActionName,
		ActivationId: l.ActivationId,
	})
}

type DatadogLogLine struct {
	Message string `json:"message,omitempty"`
	Date    int64  `json:"date,omitempty"`
	Source  string `json:"ddsource,omitempty"`
	Service string `json:"service,omitempty"`
	Tags    string `json:"ddtags,omitempty"`
}

func FormatDatadog(l LogLine) ([]byte, error) {
	if strings.HasPrefix(l.Message, "{") {
		var current map[string]interface{}
		if err := json.Unmarshal([]byte(l.Message), &current); err != nil {
			// Fall back to a raw line if the JSON can't be parsed.
			return formatDatadogRaw(l)
		}
		current["date"] = l.Time.UnixNano() / int64(time.Millisecond)
		current["ddsource"] = l.ActionName
		current["ddtags"] = fmt.Sprintf("host:%s,activationid:%s", l.ActionName, l.ActivationId)
		current["service"] = l.ActionName

		return json.Marshal(current)
	}

	return formatDatadogRaw(l)
}

func formatDatadogRaw(l LogLine) ([]byte, error) {
	return json.Marshal(DatadogLogLine{
		Message: l.Message,
		Date:    l.Time.UnixNano() / int64(time.Millisecond),
		Source:  l.ActionName,
		Service: l.ActionName,
		Tags:    fmt.Sprintf("host:%s,activationid:%s", l.ActionName, l.ActivationId),
	})
}

type RemoteLogger interface {
	// Send sends a logline to the remote service. Implementations can choose to batch
	// lines.
	Send(LogLine) error
}

// httpLogger sends a logline per HTTP request. No batching is done.
// TODO: Does batching help with log ordering?
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
