package textnorm

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  Smoked   RIBS ": "smoked ribs",
		"עוף":              "עופ", // final peh
		"עופות":            "עופות",
		"שָׁלוֹם":          "שלומ", // niqqud stripped, final mem mapped
		"ＦＵＬＬ ｗｉｄｔｈ":       "full width",
		"ﬁne":              "fine",    // NFKC ligature
		"בֶּן־גֻּרְיוֹן":   "בנגריונ", // maqaf U+05BE is inside the stripped range
		"\tמיץ\nלימון":     "מיצ לימונ",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	// the point of the final-letter mapping
	if Normalize("עוף") == Normalize("עופות") {
		t.Fatal("sanity")
	}
	if got := Normalize("עוף"); got != "עופ" || !contains(Normalize("עופות"), got) {
		t.Fatalf("עוף must be a substring of עופות after normalization: %q vs %q", got, Normalize("עופות"))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTermsAndMatch(t *testing.T) {
	long, short := Terms(`Smoked  ri "ribs"`)
	if !reflect.DeepEqual(long, []string{"smoked", `"ribs"`}) || !reflect.DeepEqual(short, []string{"ri"}) {
		t.Fatalf("%v %v", long, short)
	}
	if got := MatchExpr(long); got != `"smoked" AND """ribs"""` {
		t.Fatalf("quoting: %s", got)
	}
	if MatchExpr(nil) != "" {
		t.Fatal("empty")
	}
	if l, s := Terms("  "); len(l)+len(s) != 0 {
		t.Fatal("blank query")
	}
	if got := EscapeLike(`50%_off\`); got != `50\%\_off\\` {
		t.Fatalf("EscapeLike %q", got)
	}
}

func TestSourceKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.Example.com/ribs/?utm_source=x#top", "example.com/ribs"},
		{"http://example.com/ribs", "example.com/ribs"},
		{"https://example.com:443/ribs", "example.com/ribs"},
		{"http://example.com:80/ribs", "example.com/ribs"},
		{"https://example.com:8443/ribs", "example.com:8443/ribs"},
		{"https://user:pw@example.com/ribs", "example.com/ribs"},
		{"https://example.com", "example.com"},
		{"https://example.com/", "example.com"},
		{"https://example.com/a/./b/../ribs/", "example.com/a/ribs"},
		{"https://example.com/%72ibs", "example.com/ribs"},
		{"https://example.com/a%2fb", "example.com/a%2Fb"},
		{"https://example.com/r?b=2&a=1&utm_medium=m&fbclid=z&ref=q", "example.com/r?a=1&b=2"},
		{"https://example.com/r?id=7&UTM_Source=x", "example.com/r?id=7"},
		{"https://BÜCHER.example/ribs", "xn--bcher-kva.example/ribs"},
		{"  https://example.com/ribs  ", "example.com/ribs"},
		{"", ""},
		{"example.com/ribs", ""},
		{"ftp://example.com/ribs", ""},
		{"https:///ribs", ""},
		{"https://exa mple.com/ribs", ""},
	}
	for _, c := range cases {
		if got := SourceKey(c.in); got != c.want {
			t.Errorf("SourceKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if SourceKey("http://www.example.com/x/") != SourceKey("https://example.com/x?utm_source=a") {
		t.Error("equivalent URLs must share a key")
	}
}
