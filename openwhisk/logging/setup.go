package logging

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	logDestinationsEnv = "LOG_DESTINATIONS"

	// This value is somewhat arbitrarily set now. It's low'ish to allow the logs to be written
	// in parallel to the actual action running to amortize the timing cost of writing the logs
	// to the backend.
	logBatchInterval = 100 * time.Millisecond
	// Datadog limits the size of a batch to 5 MB, so we leave some slack to that.
	logBatchSizeLimit = 4 * 1024 * 1024
)

func RemoteLoggerFromEnv(env map[string]string) (RemoteLogger, error) {
	if env[logDestinationsEnv] == "" {
		return nil, nil
	}

	var logDestinations []logDestination
	if err := json.Unmarshal([]byte(env[logDestinationsEnv]), &logDestinations); err != nil {
		return nil, fmt.Errorf("failed to parse %q into valid log destinations: %w", logDestinationsEnv, err)
	}

	if len(logDestinations) == 0 {
		return nil, nil
	}

	// TODO(SERVERLESS-958): Actually support multiple endpoints.
	d := logDestinations[0]
	if d.Logtail != nil {
		if d.Logtail.Token == "" {
			return nil, errors.New("logtail.token has to be defined")
		}

		logger := defaultRemoteLogger()
		logger.url = "https://in.logtail.com"
		logger.headers = map[string]string{"Authorization": fmt.Sprintf("Bearer %s", d.Logtail.Token)}
		logger.format = formatLogtail
		logger.batchType = batchTypeArray

		return logger, nil
	} else if d.Papertrail != nil {
		if d.Papertrail.Token == "" {
			return nil, errors.New("papertrail.token has to be defined")
		}

		logger := defaultRemoteLogger()
		logger.url = "https://logs.collector.solarwinds.com/v1/logs"
		logger.headers = map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(":"+d.Papertrail.Token)))}
		logger.format = formatLogtail // TODO: Is there a better format for papertrail?
		logger.batchType = batchTypeNewline

		return logger, nil
	} else if d.Datadog != nil {
		if d.Datadog.ApiKey == "" || d.Datadog.Endpoint == "" {
			return nil, errors.New("datadog.endpoint and datadog.api_key have to be defined")
		}

		logger := defaultRemoteLogger()
		logger.url = fmt.Sprintf("%s/api/v2/logs", d.Datadog.Endpoint)
		logger.headers = map[string]string{"DD-API-KEY": d.Datadog.ApiKey}
		logger.format = formatDatadog
		logger.batchType = batchTypeArray

		return logger, nil
	} else {
		return nil, fmt.Errorf("invalid log destinations value in %q: either logtail, papertrail or datadog must be set", logDestinationsEnv)
	}
}

func defaultRemoteLogger() *batchingHttpLogger {
	return &batchingHttpLogger{
		http:           http.DefaultClient,
		batchInterval:  logBatchInterval,
		batchSizeLimit: logBatchSizeLimit,
		execAfter:      time.AfterFunc,
	}
}

// logDestination implements the same type hierarchy as AP, see
// https://docs.digitalocean.com/products/app-platform/references/app-specification-reference/#ref-services-log_destinations
type logDestination struct {
	Name       string                    `json:"message,omitempty"`
	Datadog    *logDestinationDatadog    `json:"datadog,omitempty"`
	Papertrail *logDestinationPapertrail `json:"papertrail,omitempty"`
	Logtail    *logDestinationLogtail    `json:"logtail,omitempty"`
}

type logDestinationDatadog struct {
	Endpoint string `json:"endpoint,omitempty"`
	ApiKey   string `json:"api_key,omitempty"`
}

type logDestinationPapertrail struct {
	// Endpoint string `json:"endpoint,omitempty"` // AP uses this but we can't due to not having syslog support (yet).
	Token string `json:"token,omitempty"`
}

type logDestinationLogtail struct {
	Token string `json:"token,omitempty"`
}
