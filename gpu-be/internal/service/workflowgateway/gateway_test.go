package workflowgateway

import "testing"

func TestLegacySRTMultilineAndTimes(t *testing.T) {
	c := ParseSRT("1\r\n00:01:02,125 --> 00:01:03,500\r\nПервый\r\nвторой\r\n\r\n2\r\n00:01:04,000 --> 00:01:05,000\r\nТретий\r\n")
	if len(c) != 2 || c[0].Start != 62.125 || c[0].End != 63.5 || c[0].Text != "Первый\nвторой" {
		t.Fatalf("cues=%+v", c)
	}
}
