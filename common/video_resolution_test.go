package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeVideoResolutionKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: " 1080P ", want: "1080p", ok: true},
		{input: "4K", want: "4k", ok: true},
		{input: "1920x1080", ok: false},
		{input: "uhd", ok: false},
	}
	for _, tc := range tests {
		got, err := NormalizeVideoResolutionKey(tc.input)
		if tc.ok {
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		} else {
			assert.Error(t, err)
		}
	}
}

func TestNormalizeVideoResolutionKeyRejectsEmptyAndNonCanonicalValues(t *testing.T) {
	for _, value := range []string{"", "   ", "99p", "100000p", "0k", "01k", "720", "p", "1920X1080"} {
		t.Run(value, func(t *testing.T) {
			_, err := NormalizeVideoResolutionKey(value)
			assert.Error(t, err)
		})
	}
}
