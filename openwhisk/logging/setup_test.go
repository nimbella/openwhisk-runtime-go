package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoteLoggerSetup(t *testing.T) {
	tests := []struct {
		service string
		env     map[string]string
	}{{
		service: "logtail",
		env: map[string]string{
			logtailTokenEnv: "testtoken",
		},
	}, {
		service: "papertrail",
		env: map[string]string{
			papertrailTokenEnv: "testtoken",
		},
	}, {
		service: "datadog",
		env: map[string]string{
			datadogSiteEnv:   "testsite.com",
			datadogApiKeyEnv: "testtoken",
		},
	}}

	for _, test := range tests {
		t.Run(test.service, func(t *testing.T) {
			test.env["LOG_DESTINATION_SERVICE"] = test.service
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
}

func TestRemoteLoggerFromEnvSetupErrors(t *testing.T) {
	tests := []struct {
		service       string
		wantErrorLine string
	}{{
		service:       "logtail",
		wantErrorLine: `"LOGTAIL_SOURCE_TOKEN" has to be an environment variable of the action`,
	}, {
		service:       "papertrail",
		wantErrorLine: `"PAPERTRAIL_TOKEN" has to be an environment variable of the action`,
	}, {
		service:       "datadog",
		wantErrorLine: `"DD_SITE" and "DD_API_KEY" have to be an environment variable of the action`,
	}}

	for _, test := range tests {
		t.Run(test.service, func(t *testing.T) {
			logger, err := RemoteLoggerFromEnv(map[string]string{"LOG_DESTINATION_SERVICE": test.service})
			assert.Nil(t, logger)
			assert.EqualError(t, err, test.wantErrorLine)
		})
	}
}
