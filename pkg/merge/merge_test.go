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

func TestMergeMapsWithDeletion(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())

	t.Run("removes keys not in desired", func(t *testing.T) {
		desired := map[string]interface{}{
			"a": "value1",
			"b": "value2",
		}
		existing := map[string]interface{}{
			"a": "value1",
			"b": "value2",
			"c": "value3", // Should be removed
		}

		res, err := MergeMapsWithDeletion(desired, existing, log)
		assert.NoError(t, err)
		assert.Equal(t, "value1", res["a"])
		assert.Equal(t, "value2", res["b"])
		assert.NotContains(t, res, "c")
	})

	t.Run("removes nested keys not in desired", func(t *testing.T) {
		desired := map[string]interface{}{
			"config": map[string]interface{}{
				"x": 1,
			},
		}
		existing := map[string]interface{}{
			"config": map[string]interface{}{
				"x": 1,
				"y": 2, // Should be removed
			},
		}

		res, err := MergeMapsWithDeletion(desired, existing, log)
		assert.NoError(t, err)
		config, ok := res["config"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, 1, config["x"])
		assert.NotContains(t, config, "y")
	})

	t.Run("preserves desired values when existing is nil", func(t *testing.T) {
		desired := map[string]interface{}{
			"a": "value1",
			"b": "value2",
		}

		res, err := MergeMapsWithDeletion(desired, nil, log)
		assert.NoError(t, err)
		assert.Equal(t, "value1", res["a"])
		assert.Equal(t, "value2", res["b"])
	})

	t.Run("returns nil when desired is nil", func(t *testing.T) {
		existing := map[string]interface{}{
			"a": "value1",
		}

		res, err := MergeMapsWithDeletion(nil, existing, log)
		assert.NoError(t, err)
		assert.Nil(t, res)
	})

	t.Run("merges nested objects recursively", func(t *testing.T) {
		desired := map[string]interface{}{
			"level1": map[string]interface{}{
				"level2": map[string]interface{}{
					"a": 1,
				},
			},
		}
		existing := map[string]interface{}{
			"level1": map[string]interface{}{
				"level2": map[string]interface{}{
					"a": 1,
					"b": 2, // Should be removed
				},
				"other": "value", // Should be removed
			},
		}

		res, err := MergeMapsWithDeletion(desired, existing, log)
		assert.NoError(t, err)
		level1, ok := res["level1"].(map[string]interface{})
		assert.True(t, ok)
		assert.NotContains(t, level1, "other")
		level2, ok := level1["level2"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, 1, level2["a"])
		assert.NotContains(t, level2, "b")
	})
}
