package api

import "testing"

// A meter never leaves its box and never divides by nothing: an empty ceiling is an empty bar, a
// count past its ceiling is a full one, and between them the share is truncated rather than
// rounded, so a bar never reads full before it is.
func TestAMeterIsClampedAndNeverDividesByNothing(t *testing.T) {
	for _, c := range []struct {
		n, of int64
		want  int
	}{
		{0, 0, 0}, {5, 0, 0}, {-1, 10, 0}, {0, 10, 0},
		{1, 3, 33}, {99, 100, 99}, {100, 100, 100}, {150, 100, 100},
	} {
		if got := share(c.n, c.of); got != c.want {
			t.Errorf("share(%d, %d) = %d, want %d", c.n, c.of, got, c.want)
		}
	}
}
