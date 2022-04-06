package logging

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDatadogJSON(t *testing.T) {
	by, err := formatDatadog(logDestinationAttributes{"testapp", "testfunct"})(LogLine{
		Message:      `{"message": "foo", "attribute": "bar"}`,
		Time:         time.Unix(0, 0),
		Stream:       "stdout",
		ActivationId: "testid",
	})
	assert.NoError(t, err, "failed to format log line")

	var got map[string]interface{}
	assert.NoError(t, json.Unmarshal(by, &got), "failed to unmarshal log line")

	want := map[string]interface{}{
		"message":   "foo", // This and 'attribute' are flattened into the structure.
		"attribute": "bar",
		"date":      float64(0), // Generic parsing transforms numbers into float64.
		"ddtags":    "host:testapp,activationid:testid",
		"ddsource":  "testapp",
		"service":   "testfunct",
	}

	assert.Equal(t, want, got)
}

func TestFormatDatadogJSONFallback(t *testing.T) {
	by, err := formatDatadog(logDestinationAttributes{"testapp", "testfunct"})(LogLine{
		Message:      `{ha... i'm not actually JSON :)`,
		Time:         time.Unix(0, 0),
		Stream:       "stdout",
		ActivationId: "testid",
	})
	assert.NoError(t, err, "failed to format log line")

	var got map[string]interface{}
	assert.NoError(t, json.Unmarshal(by, &got), "failed to unmarshal log line")

	want := map[string]interface{}{
		"message":  "{ha... i'm not actually JSON :)", // This and 'attribute' are flattened into the structure.
		"ddtags":   "host:testapp,activationid:testid",
		"ddsource": "testapp",
		"service":  "testfunct",
	}

	assert.Equal(t, want, got)
}
