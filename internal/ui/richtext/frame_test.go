package richtext

import "testing"

func TestSanitize_DropsBidiOverridesAndIsolates(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in   string
		want string
	}{
		"a right-to-left override is dropped": {
			in: "a\u202eb", want: "ab",
		},
		"every bidi embedding and override is dropped": {
			in: "\u202a\u202b\u202c\u202d\u202ex", want: "x",
		},
		"every bidi isolate is dropped": {
			in: "\u2066\u2067\u2068\u2069x", want: "x",
		},
		"an existing C0 control is still dropped": {
			in: "a\x07b", want: "ab",
		},
		"clean text is untouched": {
			in: "ship the release", want: "ship the release",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := sanitize(tc.in); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
