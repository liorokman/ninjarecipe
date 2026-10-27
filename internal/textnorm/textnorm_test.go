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
