package stages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildRepairTsArgs(t *testing.T) {
	args := buildRepairTsArgs("input.ts", "output.ts")
	joined := strings.Join(args, " ")

	assert.Contains(t, joined, "-fflags +genpts+igndts+discardcorrupt")
	assert.Contains(t, joined, "-map 0:v:0?")
	assert.Contains(t, joined, "-map 0:a?")
	assert.Contains(t, joined, "-c copy")
	assert.Contains(t, joined, "-avoid_negative_ts make_zero")
	assert.Contains(t, joined, "-mpegts_flags +resend_headers+initial_discontinuity")
	assert.Equal(t, "output.ts", args[len(args)-1])
}

func TestParseMediaStreams(t *testing.T) {
	output := `
  Stream #0:0: Video: h264, yuv420p, 1920x1080
  Stream #0:1: Audio: aac, 48000 Hz, stereo
`
	streams := parseMediaStreams(output)
	assert.True(t, streams.video)
	assert.True(t, streams.audio)

	streams = parseMediaStreams("  Stream #0:0: Video: h264, yuv420p, 1280x720")
	assert.True(t, streams.video)
	assert.False(t, streams.audio)
}

func TestTruncateCommandOutput(t *testing.T) {
	assert.Equal(t, "abcdef", truncateCommandOutput("abcdef", 10))
	assert.Equal(t, "cdef", truncateCommandOutput("abcdef", 4))
	assert.Equal(t, "", truncateCommandOutput("", 4))
}
