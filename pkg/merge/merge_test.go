package merge

import (
	"testing"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/assert"
)

func TestObjectMerge(t *testing.T) {
	// Given
	original := map[string]interface{}{
		"kcp": map[string]interface{}{
			"enabled": true,
			"url":     "https://kcp.example.com",
			"domains": []string{"example.com", "example.org"},
		},
		"logLevel": "info",
	}

	overwrite := map[string]interface{}{
		"kcp": map[string]interface{}{
			"enabled": false,
			"domains": []string{"example.com", "example2.org"},
		},
	}
	log, _ := logger.New(logger.DefaultConfig())
	res, err := MergeMaps(original, overwrite, log)
	assert.NoError(t, err)
	assert.False(t, res["kcp"].(map[string]interface{})["enabled"].(bool))
	assert.NotNil(t, res["kcp"].(map[string]interface{})["url"])
	assert.Equal(t, "https://kcp.example.com", res["kcp"].(map[string]interface{})["url"].(string))
	assert.Len(t, res["kcp"].(map[string]interface{})["domains"].([]string), 2)
	assert.Equal(t, "example.com", res["kcp"].(map[string]interface{})["domains"].([]string)[0])
	assert.Equal(t, "example2.org", res["kcp"].(map[string]interface{})["domains"].([]string)[1])
}
