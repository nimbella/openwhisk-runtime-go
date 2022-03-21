package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoteLoggerSetup(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{{
		name: "logtail",
		env: map[string]string{
			logDestinationsEnv: `[{"name": "foo", "logtail": {"token": "testtoken"}}]`,
		},
	}, {
		name: "papertrail",
		env: map[string]string{
			logDestinationsEnv: `[{"name": "foo", "papertrail": {"token": "testtoken"}}]`,
		},
	}, {
		name: "datadog",
		env: map[string]string{
			logDestinationsEnv: `[{"name": "foo", "datadog": {"endpoint": "testendpoint", "api_key": "testkey"}}]`,
		},
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger, err := RemoteLoggerFromEnv(test.env)
			assert.NoError(t, err)
			assert.NotNil(t, logger)
		})
	}
}

func TestRemoteLoggerSetupNoLogger(t *testing.T) {
	logger, err := RemoteLoggerFromEnv(map[string]string{})
	assert.NoError(t, err)
	assert.Nil(t, logger)

	logger, err = RemoteLoggerFromEnv(map[string]string{logDestinationsEnv: ""})
	assert.NoError(t, err)
	assert.Nil(t, logger)

	logger, err = RemoteLoggerFromEnv(map[string]string{logDestinationsEnv: "[]"})
	assert.NoError(t, err)
	assert.Nil(t, logger)
}

func TestRemoteLoggerFromEnvSetupErrors(t *testing.T) {
	tests := []struct {
		name          string
		env           map[string]string
		wantErrorLine string
	}{{
		name: "none",
		env: map[string]string{
			logDestinationsEnv: `[{}]`,
		},
		wantErrorLine: `invalid log destinations value in "LOG_DESTINATIONS": either logtail, papertrail or datadog must be set`,
	}, {
		name: "borked",
		env: map[string]string{
			logDestinationsEnv: `{}`,
		},
		wantErrorLine: `failed to parse "LOG_DESTINATIONS" into valid log destinations: json: cannot unmarshal object into Go value of type []logging.logDestination`,
	}, {
		name: "logtail",
		env: map[string]string{
			logDestinationsEnv: `[{"logtail": {}}]`,
		},
		wantErrorLine: "logtail.token has to be defined",
	}, {
		name: "papertrail",
		env: map[string]string{
			logDestinationsEnv: `[{"papertrail": {}}]`,
		},
		wantErrorLine: "papertrail.token has to be defined",
	}, {
		name: "datadog",
		env: map[string]string{
			logDestinationsEnv: `[{"datadog": {}}]`,
		},
		wantErrorLine: "datadog.endpoint and datadog.api_key have to be defined",
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger, err := RemoteLoggerFromEnv(test.env)
			assert.Nil(t, logger)
			assert.EqualError(t, err, test.wantErrorLine)
		})
	}
}
