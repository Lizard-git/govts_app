package appversion

import "testing"

func TestParseAndCompare(t *testing.T) {
	low, err := Parse("0.1.84")
	if err != nil {
		t.Fatal(err)
	}
	high, err := Parse("0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if low.String() != "0.1.84" || high <= low {
		t.Fatalf("versions = %s, %s", low, high)
	}
	for _, invalid := range []string{"", "1.2", "01.2.3", "1.256.0", "1.2.65536", "1.2.3-beta"} {
		if _, err := Parse(invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
}
