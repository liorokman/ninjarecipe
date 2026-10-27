package recipetext

import (
	"strings"
	"testing"

	"cooking/internal/domain"
)

func TestParseIngredientLine(t *testing.T) {
	cases := []struct {
		in     string
		amount float64
		unit   string
		item   string
	}{
		{"2 tbsp smoked paprika", 2, "tbsp", "smoked paprika"},
		{"1.5 kg beef short ribs", 1.5, "kg", "beef short ribs"},
		{"1,5 kg beef", 1.5, "kg", "beef"},
		{"500g flour", 500, "g", "flour"},
		{"1/2 tsp salt", 0.5, "tsp", "salt"},
		{"1 1/2 cups milk", 1.5, "", "cups milk"},
		{"½ tsp pepper", 0.5, "tsp", "pepper"},
		{"¾ l water", 0.75, "l", "water"},
		{"2 eggs", 2, "", "eggs"},
		{"2 garlic cloves", 2, "", "garlic cloves"},
		{"3 Tablespoons butter", 3, "tbsp", "butter"},
		{"250 גרם קמח", 250, "g", "קמח"},
		{"2 כפות סוכר", 2, "tbsp", "סוכר"},
		{"1 ק\"ג עוף", 1, "kg", "עוף"},
		{"3 יח' ביצים", 3, "pcs", "ביצים"},
		{"1 קורט מלח", 1, "pinch", "מלח"},
		{"2 pinch salt", 2, "pinch", "salt"},
		{"1½ tsp salt", 1.5, "tsp", "salt"},
	}
	for _, c := range cases {
		got := ParseIngredientLine(c.in)
		if got.Amount == nil || diff(*got.Amount, c.amount) || got.Unit != c.unit || got.Item != c.item || got.RawText != c.in {
			t.Errorf("%q -> %+v", c.in, got)
		}
	}
	for _, in := range []string{"salt to taste", "a handful of herbs", "0 tsp salt", "oil", "טחינה לפי הטעם"} {
		got := ParseIngredientLine(in)
		if got.Amount != nil || got.Unit != "" || got.Item != in || got.RawText != in {
			t.Errorf("%q should stay unparsed: %+v", in, got)
		}
	}
}

func diff(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d > 0.001
}

func TestParseIngredientsSections(t *testing.T) {
	text := "# Dough\n500 g flour\n\n  2 tsp salt \r\n# Topping\nmozzarella\n#\n1 egg\n"
	got := ParseIngredients(text)
	if len(got) != 4 {
		t.Fatalf("%d lines: %+v", len(got), got)
	}
	if got[0].Section != "Dough" || got[1].Section != "Dough" || got[2].Section != "Topping" || got[3].Section != "" {
		t.Fatalf("sections: %+v", got)
	}
	for i, g := range got {
		if g.Position != i+1 {
			t.Fatal("positions")
		}
	}
	// round trip through the editor textarea
	again := ParseIngredients(FormatIngredients(got))
	if len(again) != 4 || again[2].Section != "Topping" || again[0].RawText != "500 g flour" {
		t.Fatalf("round trip: %+v", again)
	}
	if len(ParseIngredients("  \n\n")) != 0 {
		t.Fatal("empty")
	}
}

func TestParseStepsTimers(t *testing.T) {
	text := "Rub the ribs [10:00]\n\nSmoke for three hours [3:00:00]\nRest [05:30]\nNo timer [99:99]\nodd [1:2]"
	got := ParseSteps(text)
	if len(got) != 5 {
		t.Fatalf("%d steps %+v", len(got), got)
	}
	if got[0].Text != "Rub the ribs" || got[0].TimerSeconds == nil || *got[0].TimerSeconds != 600 {
		t.Fatalf("mm:ss: %+v", got[0])
	}
	if *got[1].TimerSeconds != 10800 || got[1].Text != "Smoke for three hours" {
		t.Fatalf("h:mm:ss: %+v", got[1])
	}
	if *got[2].TimerSeconds != 330 {
		t.Fatalf("05:30: %+v", got[2])
	}
	if got[3].TimerSeconds != nil || !strings.Contains(got[3].Text, "[99:99]") {
		t.Fatalf("invalid minutes/seconds are plain text: %+v", got[3])
	}
	if got[4].TimerSeconds != nil {
		t.Fatalf("malformed: %+v", got[4])
	}
	if FormatTimer(600) != "10:00" || FormatTimer(3600) != "1:00:00" || FormatTimer(5) != "00:05" {
		t.Fatalf("FormatTimer %s %s", FormatTimer(600), FormatTimer(3600))
	}
	rt := ParseSteps(FormatSteps(got[:3]))
	if len(rt) != 3 || *rt[0].TimerSeconds != 600 || *rt[1].TimerSeconds != 10800 {
		t.Fatalf("round trip: %+v", rt)
	}
	if got := ParseSteps("[10:00]"); len(got) != 1 {
		t.Fatal("a step that is only a timer keeps its text")
	}
}

func TestTags(t *testing.T) {
	tags, bad := SplitTags(" BBQ , weekend,bbq,  Slow   Cook ,,ＦＵＬＬ")
	want := []string{"bbq", "weekend", "slow cook", "full"}
	if bad || strings.Join(tags, "|") != strings.Join(want, "|") {
		t.Fatalf("%v bad=%v", tags, bad)
	}
	if _, ok := NormalizeTag(strings.Repeat("a", 41)); ok {
		t.Fatal("41 chars")
	}
	if _, ok := NormalizeTag("   "); ok {
		t.Fatal("blank")
	}
	if n, ok := NormalizeTag("צמחוני"); !ok || n != "צמחוני" {
		t.Fatal("hebrew tag")
	}
	var _ domain.Ingredient
}
