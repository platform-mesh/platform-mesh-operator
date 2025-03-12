package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewFromEnv(t *testing.T) {
	// Given
	// When

	_, err := NewFromEnv()
	// Then
	assert.NoError(t, err)
}
