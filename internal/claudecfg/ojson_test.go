package claudecfg

import "testing"

func TestRoundTripPreservesOrderAndNumbers(t *testing.T) {
	in := `{
  "zeta": 1,
  "alpha": {
    "b": [
      1.50,
      true,
      null,
      "x<y>"
    ],
    "a": {}
  },
  "big": 12345678901234567890,
  "empty": []
}
`
	o, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := o.Marshal()
	if string(out) != in {
		t.Fatalf("round trip changed file:\n%s", out)
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{`[1,2]`, `{"a":1} trailing`, `{"a":`, `"str"`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) accepted", in)
		}
	}
	o, err := Parse([]byte("  \n"))
	if err != nil || len(o.Keys()) != 0 {
		t.Fatalf("empty file: %v %v", o, err)
	}
}
