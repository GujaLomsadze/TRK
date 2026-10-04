package derive

import "testing"

func TestCollisions(t *testing.T) {
	now := int64(60 * min)
	cases := []struct {
		name    string
		touches []FileTouch
		want    int
	}{
		{"edit + read by two sessions", []FileTouch{{"a", "/r/x.go", now - min, true}, {"b", "/r/x.go", now - 2*min, false}}, 1},
		{"two edits", []FileTouch{{"a", "/r/x.go", now - min, true}, {"b", "/r/x.go", now - min, true}}, 1},
		{"reads only", []FileTouch{{"a", "/r/x.go", now - min, false}, {"b", "/r/x.go", now - min, false}}, 0},
		{"same session twice", []FileTouch{{"a", "/r/x.go", now - min, true}, {"a", "/r/x.go", now, true}}, 0},
		{"outside window", []FileTouch{{"a", "/r/x.go", now - 11*min, true}, {"b", "/r/x.go", now - min, true}}, 0},
		{"different files", []FileTouch{{"a", "/r/x.go", now, true}, {"b", "/r/y.go", now, true}}, 0},
		{"unclean paths match", []FileTouch{{"a", "/r/./x.go", now, true}, {"b", "/r/x.go", now, true}}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Collisions(c.touches, now)
			if len(got) != c.want {
				t.Fatalf("got %+v, want %d", got, c.want)
			}
			if c.want == 1 && (len(got[0].Sessions) != 2 || got[0].Sessions[0] != "a") {
				t.Fatalf("sessions = %v", got[0].Sessions)
			}
		})
	}
}
