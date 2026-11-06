package kvm

import "testing"

func TestQualityParseAndString(t *testing.T) {
	for in, want := range map[string]Quality{"high": QualityHigh, "HIGH": QualityHigh, "low": QualityLow, "Low": QualityLow} {
		q, ok := ParseQuality(in)
		if !ok || q != want {
			t.Errorf("%q: %v %v", in, q, ok)
		}
		if q2, ok := ParseQuality(q.String()); !ok || q2 != q {
			t.Errorf("round trip %q", in)
		}
	}
	if _, ok := ParseQuality("medium"); ok {
		t.Fatal("medium accepted")
	}
	if Quality(0) != QualityHigh {
		t.Fatal("zero value is not High")
	}
	if QualityHigh.bytesPerPixel() != 2 || QualityLow.bytesPerPixel() != 1 {
		t.Fatal("bytesPerPixel")
	}
}
