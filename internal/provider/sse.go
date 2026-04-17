package provider

import (
	"bufio"
	"io"
)

const maxSSELineBytes = 1024 * 1024 // 1 MiB

// NewSSEScanner returns a scanner configured for larger-than-default SSE lines.
// Provider APIs can emit large JSON chunks that exceed bufio.Scanner's 64 KiB default.
func NewSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, maxSSELineBytes)
	return s
}

