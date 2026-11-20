package feishu

import "testing"

func TestNormalizePageSize(t *testing.T) {
	cases := []struct {
		in      int
		want    int
		wantErr bool
	}{
		{1, 1, false},
		{50, 50, false},
		{51, 50, false},
		{1000, 50, false},
		{0, 0, true},
		{-1, 0, true},
	}
	for _, c := range cases {
		got, err := normalizePageSize(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("normalizePageSize(%d) err=%v, wantErr=%v", c.in, err, c.wantErr)
		}
		if got != c.want {
			t.Fatalf("normalizePageSize(%d)=%d, want %d", c.in, got, c.want)
		}
	}
}
