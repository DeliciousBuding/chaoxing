package upstream

import "testing"

// A malformed varint length must not be converted to a negative int and used
// as a slice bound.
func TestParseProtobufRejectsOversizedLength(t *testing.T) {
	payload := []byte{0x0a}
	payload = append(payload, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}...)
	got := parseProtobuf(payload)
	if len(got) != 0 {
		t.Fatalf("oversized length decoded: %+v", got)
	}
}
