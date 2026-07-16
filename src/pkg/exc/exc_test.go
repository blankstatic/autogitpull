package exc

import "testing"

func TestLimitedBufferBoundsRetainedOutput(t *testing.T) {
	buffer := &limitedBuffer{limit: 5}
	for _, part := range []string{"abc", "def", "ghi"} {
		n, err := buffer.Write([]byte(part))
		if err != nil || n != len(part) {
			t.Fatalf("Write(%q) = %d, %v", part, n, err)
		}
	}
	if got := buffer.String(); got != "abcde" {
		t.Fatalf("retained output = %q", got)
	}
	if !buffer.truncated {
		t.Fatal("expected truncation to be reported")
	}
}

func TestUnlimitedBufferKeepsAllOutput(t *testing.T) {
	buffer := &limitedBuffer{}
	_, _ = buffer.Write([]byte("complete output"))
	if got := buffer.String(); got != "complete output" || buffer.truncated {
		t.Fatalf("unexpected unlimited result %q, truncated=%v", got, buffer.truncated)
	}
}
