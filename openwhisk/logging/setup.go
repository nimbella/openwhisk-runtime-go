package logging

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	logDestinationsEnv = "LOG_DESTINATIONS"
	actionNameEnv      = "__OW_ACTION_NAME"

	// This value is somewhat arbitrarily set now. It's low'ish to allow the logs to be written
	// in parallel to the actual action running to amortize the timing cost of writing the logs
	// to the backend.
	logBatchInterval = 100 * time.Millisecond
	// Datadog limits the size of a batch to 5 MB, so we leave some slack to that.
	logBatchSizeLimit = 4 * 1024 * 1024
)

func RemoteLoggerFromEnv(env map[string]string) ([]RemoteLogger, error) {
	if env[logDestinationsEnv] == "" {
		return nil, nil
	}
	valueToParse := env[logDestinationsEnv]
	var logDestinations []logDestination
	var logAttributes logDestinationAttributes
	if strings.HasPrefix(valueToParse, "[") {
		// It's an array, the "smaller" format of just using the log destinations applies.
		if err := json.Unmarshal([]byte(valueToParse), &logDestinations); err != nil {
			return nil, fmt.Errorf("failed to parse %q into valid log destinations: %w", logDestinationsEnv, err)
		}

		// Fall back to all action name attributes if no other attributes are passed.
		actionName := env[actionNameEnv]
		logAttributes = logDestinationAttributes{
			AppName:       actionName,
			ComponentName: actionName,
		}
	} else {
		// The "richer" format, containing more metadata, applies.
		var logDestinationConfig logDestinationConfig
		if err := json.Unmarshal([]byte(env[logDestinationsEnv]), &logDestinationConfig); err != nil {
			return nil, fmt.Errorf("failed to parse %q into valid log destinations: %w", logDestinationsEnv, err)
		}
		logDestinations = logDestinationConfig.Destinations
		logAttributes = logDestinationConfig.Attributes
	}

	if len(logDestinations) == 0 {
		return nil, nil
	}

	loggers := make([]RemoteLogger, 0, len(logDestinations))
	for _, d := range logDestinations {
		if d.Logtail != nil {
			if d.Logtail.Token == "" {
				return nil, errors.New("logtail.token has to be defined")
			}

			logger := defaultRemoteLogger()
			logger.url = "https://in.logtail.com"
			logger.headers = map[string]string{"Authorization": fmt.Sprintf("Bearer %s", d.Logtail.Token)}
			logger.format = formatLogtail(logAttributes)
			logger.batchType = batchTypeArray

			loggers = append(loggers, logger)
		} else if d.Papertrail != nil {
			if d.Papertrail.Token == "" {
				return nil, errors.New("papertrail.token has to be defined")
			}

			logger := defaultRemoteLogger()
			logger.url = "https://logs.collector.solarwinds.com/v1/logs"
			logger.headers = map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(":"+d.Papertrail.Token)))}
			logger.format = formatLogtail(logAttributes) // TODO: Is there a better format for papertrail?
			logger.batchType = batchTypeNewline

			loggers = append(loggers, logger)
		} else if d.Datadog != nil {
			if d.Datadog.ApiKey == "" || d.Datadog.Endpoint == "" {
				return nil, errors.New("datadog.endpoint and datadog.api_key have to be defined")
			}

			logger := defaultRemoteLogger()
			logger.url = fmt.Sprintf("%s/api/v2/logs", d.Datadog.Endpoint)
			logger.headers = map[string]string{"DD-API-KEY": d.Datadog.ApiKey}
			logger.format = formatDatadog(logAttributes)
			logger.batchType = batchTypeArray

			loggers = append(loggers, logger)
		} else {
			return nil, fmt.Errorf("invalid log destinations value in %q: either logtail, papertrail or datadog must be set", logDestinationsEnv)
		}
	}
	return loggers, nil
}

func defaultRemoteLogger() *batchingHttpLogger {
	return &batchingHttpLogger{
		http:           http.DefaultClient,
		batchInterval:  logBatchInterval,
		batchSizeLimit: logBatchSizeLimit,
		execAfter:      time.AfterFunc,
	}
}

type logDestinationConfig struct {
	Destinations []logDestination         `json:"destinations,omitempty"`
	Attributes   logDestinationAttributes `json:"attributes,omitempty"`
}

type logDestinationAttributes struct {
	AppName       string `json:"app_name,omitempty"`
	ComponentName string `json:"component_name,omitempty"`
}

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
	Token string `json:"token,omitempty"`
}

type logDestinationLogtail struct {
	Token string `json:"token,omitempty"`
}
