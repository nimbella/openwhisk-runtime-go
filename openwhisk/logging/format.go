package logging

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type logtailLogLine struct {
	Message      string `json:"message,omitempty"`
	Time         string `json:"dt,omitempty"`
	Host         string `json:"host,omitempty"`
	AppName      string `json:"appname,omitempty"`
	ActivationId string `json:"activationId,omitempty"`
}

func formatLogtail(metadata logDestinationAttributes) func(LogLine) ([]byte, error) {
	return func(l LogLine) ([]byte, error) {
		return json.Marshal(logtailLogLine{
			Message:      l.Message,
			Time:         l.Time.UTC().Format("2006-01-02 15:04:05.000000000 MST"),
			Host:         metadata.AppName,
			AppName:      metadata.ComponentName,
			ActivationId: l.ActivationId,
		})
	}
}

type datadogLogLine struct {
	Message string `json:"message,omitempty"`
	Date    int64  `json:"date,omitempty"`
	Source  string `json:"ddsource,omitempty"`
	Service string `json:"service,omitempty"`
	Tags    string `json:"ddtags,omitempty"`
}

func formatDatadog(metadata logDestinationAttributes) func(LogLine) ([]byte, error) {
	return func(l LogLine) ([]byte, error) {
		if strings.HasPrefix(l.Message, "{") {
			var current map[string]interface{}
			if err := json.Unmarshal([]byte(l.Message), &current); err != nil {
				// Fall back to a raw line if the JSON can't be parsed.
				return formatDatadogRaw(metadata, l)
			}
			current["date"] = l.Time.UnixNano() / int64(time.Millisecond)
			current["ddsource"] = metadata.AppName
			current["ddtags"] = fmt.Sprintf("host:%s,activationid:%s", metadata.AppName, l.ActivationId)
			current["service"] = metadata.ComponentName

			return json.Marshal(current)
		}

		return formatDatadogRaw(metadata, l)
	}
}

func formatDatadogRaw(metadata logDestinationAttributes, l LogLine) ([]byte, error) {
	return json.Marshal(datadogLogLine{
		Message: l.Message,
		Date:    l.Time.UnixNano() / int64(time.Millisecond),
		Source:  metadata.AppName,
		Service: metadata.ComponentName,
		Tags:    fmt.Sprintf("host:%s,activationid:%s", metadata.AppName, l.ActivationId),
	})
}
