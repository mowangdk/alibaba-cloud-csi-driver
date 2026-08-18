//go:build !windows

package ens

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestENSEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		expected string
	}{
		{
			name:     "defaults to the China site endpoint when unset",
			env:      "",
			expected: "ens.aliyuncs.com",
		},
		{
			name:     "uses the international site endpoint from env",
			env:      "ens.ap-southeast-1.aliyuncs.com",
			expected: "ens.ap-southeast-1.aliyuncs.com",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ENS_ENDPOINT", test.env)
			assert.Equal(t, test.expected, ensEndpoint())
		})
	}
}
