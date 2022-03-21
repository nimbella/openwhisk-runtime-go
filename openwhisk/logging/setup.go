package logging

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

const (
	logDestinationServiceEnv = "LOG_DESTINATION_SERVICE"
	logtailTokenEnv          = "LOGTAIL_SOURCE_TOKEN"
	papertrailTokenEnv       = "PAPERTRAIL_TOKEN"
	datadogSiteEnv           = "DD_SITE"
	datadogApiKeyEnv         = "DD_API_KEY"

	// This value is somewhat arbitrarily set now. It's low'ish to allow the logs to be written
	// in parallel to the actual action running to amortize the timing cost of writing the logs
	// to the backend.
	logBatchInterval = 100 * time.Millisecond
	// Datadog limits the size of a batch to 5 MB, so we leave some slack to that.
	logBatchSizeLimit = 4 * 1024 * 1024
)

func RemoteLoggerFromEnv(env map[string]string) (RemoteLogger, error) {
	switch env[logDestinationServiceEnv] {
	case "logtail":
		if env[logtailTokenEnv] == "" {
			return nil, fmt.Errorf("%q has to be an environment variable of the action", logtailTokenEnv)
		}

		logger := defaultRemoteLogger()
		logger.url = "https://in.logtail.com"
		logger.headers = map[string]string{"Authorization": fmt.Sprintf("Bearer %s", env[logtailTokenEnv])}
		logger.format = formatLogtail
		logger.batchType = batchTypeArray

		return logger, nil
	case "papertrail":
		if env[papertrailTokenEnv] == "" {
			return nil, fmt.Errorf("%q has to be an environment variable of the action", papertrailTokenEnv)
		}

		logger := defaultRemoteLogger()
		logger.url = "https://logs.collector.solarwinds.com/v1/logs"
		logger.headers = map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(":"+env[papertrailTokenEnv])))}
		logger.format = formatLogtail // TODO: Is there a better format for papertrail?
		logger.batchType = batchTypeNewline

		return logger, nil
	case "datadog":
		if env[datadogSiteEnv] == "" || env[datadogApiKeyEnv] == "" {
			return nil, fmt.Errorf("%q and %q have to be an environment variable of the action", datadogSiteEnv, datadogApiKeyEnv)
		}

		logger := defaultRemoteLogger()
		logger.url = fmt.Sprintf("https://http-intake.logs.%s/api/v2/logs", env[datadogSiteEnv])
		logger.headers = map[string]string{"DD-API-KEY": env[datadogApiKeyEnv]}
		logger.format = formatDatadog
		logger.batchType = batchTypeArray

		return logger, nil
	}
	return nil, nil
}

func defaultRemoteLogger() *batchingHttpLogger {
	return &batchingHttpLogger{
		http:           http.DefaultClient,
		batchInterval:  logBatchInterval,
		batchSizeLimit: logBatchSizeLimit,
		execAfter:      time.AfterFunc,
	}
}
