package main

import "testing"

func TestBumpPatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "increments patch", input: "0.1.0", want: "0.1.1"},
		{name: "keeps major and minor", input: "12.34.99", want: "12.34.100"},
		{name: "rejects missing component", input: "1.2", wantErr: true},
		{name: "rejects suffix", input: "1.2.3-beta", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := bumpPatch(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("bumpPatch(%q) error = %v, wantErr %v", test.input, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("bumpPatch(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
