// Package fixtures provides realistic canned domain data for the UI harness (no database).
package fixtures

import (
	"time"

	"cooking/internal/domain"
)

func ip(i int) *int         { return &i }
func fp(f float64) *float64 { return &f }

// Categories are the 12 seeded categories (30 §5).
func Categories() []domain.Label {
	rows := []struct{ slug, en, he, icon string }{
		{"pizza", "Pizza", "פיצה", "🍕"}, {"bread_dough", "Bread & Dough", "לחם ובצקים", "🍞"}, {"beef", "Beef", "בקר", "🥩"},
		{"lamb", "Lamb", "טלה", "🍖"}, {"pork", "Pork", "חזיר", "🥓"}, {"poultry", "Chicken & Poultry", "עוף", "🍗"},
		{"fish_seafood", "Fish & Seafood", "דגים ופירות ים", "🐟"}, {"vegetables", "Vegetables", "ירקות", "🥦"},
		{"sides", "Sides", "תוספות", "🥔"}, {"desserts", "Desserts", "קינוחים", "🍰"},
		{"snacks_dried", "Snacks & Dried", "חטיפים ומיובשים", "🥨"}, {"other", "Other", "אחר", "🍽️"},
	}
	var out []domain.Label
	for i, r := range rows {
		out = append(out, domain.Label{ID: int64(i + 1), Type: "category", Slug: r.slug, Name: r.en, NameHe: r.he, Icon: r.icon, SortOrder: (i + 1) * 10})
	}
	return out
}

// Data is the whole fixture world.
type Data struct {
	Now     time.Time
	Users   map[string]*domain.User // alice, bob, admin
	Recipes []*domain.Recipe
	Inbox   []*domain.Recipe
	Logs    map[string][]domain.CookLog
	Favs    map[string]map[string]bool // user -> recipe -> true
	Tokens  []domain.APIToken
}

func cat(slug string) domain.Label {
	for _, c := range Categories() {
		if c.Slug == slug {
			c.Source = "user"
			return c
		}
	}
	return domain.Label{}
}
func tag(n string) domain.Label { return domain.Label{Type: "tag", Slug: n, Name: n, Source: "user"} }

func img(recipe string, i int) domain.Image {
	key := ""
	for len(key) < 32 {
		key += string("0123456789abcdef"[(i*7+len(recipe)+len(key))%16])
	}
	return domain.Image{ID: recipe + "-img" + string(rune('0'+i)), RecipeID: recipe, Key: key, Kind: "recipe", Position: i, Width: 1600, Height: 1200}
}

// New builds the fixture world relative to now.
func New(now time.Time) *Data {
	d := &Data{Now: now, Logs: map[string][]domain.CookLog{}, Favs: map[string]map[string]bool{"u-alice": {}, "u-bob": {}, "u-admin": {}}}
	d.Users = map[string]*domain.User{
		"alice": {ID: "u-alice", DisplayName: "Alice", Email: "alice@example.com", UILanguage: "en"},
		"bob":   {ID: "u-bob", DisplayName: "Bob", Email: "bob@example.com", UILanguage: "en"},
		"admin": {ID: "u-admin", DisplayName: "Lior", Email: "lior@example.com", IsAdmin: true, UILanguage: "en"},
	}
	ago := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	base := func(id, owner, ownerName, title, vis string, h int) *domain.Recipe {
		return &domain.Recipe{ID: id, OwnerID: owner, OwnerName: ownerName, Status: "active", Visibility: vis, Title: title, Language: "en",
			Version: 3, CreatedAt: ago(h + 100), UpdatedAt: ago(h), CreatedBy: owner, UpdatedBy: owner, CreatedByName: ownerName, UpdatedByName: ownerName}
	}
	// 1. Smoked ribs: alice, private, smoker then max roast, photos, timers.
	r1 := base("fx-ribs", "u-alice", "Alice", "Smoked short ribs", "private", 5)
	r1.Description = "Low and slow, finished hot for a sticky bark."
	r1.Notes = "Spritz with apple juice every hour if the bark looks dry."
	r1.Servings, r1.PrepMinutes = ip(4), ip(30)
	r1.SourceURL, r1.SourceName = "https://www.example-kitchen.com/smoked-short-ribs", "Example Kitchen"
	r1.Stages = []domain.OvenStage{
		{Position: 1, Mode: "smoker", TemperatureC: ip(120), DurationSeconds: ip(3 * 3600), Woodfire: true, PelletBlend: "robust", PelletNote: "hickory", Accessories: []string{"pro_heat_pan"}, TargetCoreTempC: ip(93), Notes: "Fat side up."},
		{Position: 2, Mode: "max_roast", TemperatureC: ip(250), DurationSeconds: ip(10 * 60), PreheatMinutes: ip(15), Accessories: []string{"pro_heat_pan", "roast_rack"}, Notes: "Brush with glaze at the start."},
	}
	r1.Ingredients = []domain.Ingredient{
		{Position: 1, Section: "Ribs", Amount: fp(1.5), Unit: "kg", Item: "beef short ribs", RawText: "1.5 kg beef short ribs"},
		{Position: 2, Section: "Ribs", Amount: fp(2), Unit: "tbsp", Item: "smoked paprika", RawText: "2 tbsp smoked paprika"},
		{Position: 3, Section: "Ribs", Amount: fp(1.5), Unit: "tsp", Item: "kosher salt", RawText: "1½ tsp kosher salt"},
		{Position: 4, Section: "Glaze", Amount: fp(0.25), Unit: "l", Item: "apple juice", RawText: "¼ l apple juice"},
		{Position: 5, Section: "Glaze", Item: "black pepper, to taste", RawText: "black pepper, to taste"},
	}
	r1.Steps = []domain.Step{
		{Position: 1, Text: "Trim the ribs and pat dry."}, {Position: 2, Text: "Rub with paprika, salt and pepper. Rest 30 minutes.", TimerSeconds: ip(1800)},
		{Position: 3, Text: "Load the smoke box with robust pellets and start the Smoker at 120 °C."},
		{Position: 4, Text: "Smoke for 3 hours.", TimerSeconds: ip(3 * 3600)},
		{Position: 5, Text: "Switch to Max Roast 250 °C and glaze.", TimerSeconds: ip(600)},
		{Position: 6, Text: "Rest 10 minutes before slicing.", TimerSeconds: ip(600)},
	}
	r1.Images = []domain.Image{img(r1.ID, 1), img(r1.ID, 2)}
	r1.Labels = []domain.Label{cat("beef"), tag("weekend"), tag("bbq")}
	// 2. Gourmet roast chicken: alice, shared.
	r2 := base("fx-chicken", "u-alice", "Alice", "Whole roast chicken", "shared", 30)
	r2.Servings = ip(4)
	r2.Stages = []domain.OvenStage{{Position: 1, Mode: "gourmet_roast", TemperatureC: ip(330), DurationSeconds: ip(20 * 60), Temperature2C: ip(140), Duration2Seconds: ip(3600), PreheatMinutes: ip(20), Accessories: []string{"pro_heat_pan", "roast_rack"}, TargetCoreTempC: ip(74)}}
	r2.Ingredients = []domain.Ingredient{{Position: 1, Amount: fp(1), Unit: "pcs", Item: "whole chicken, about 1.6 kg", RawText: "1 whole chicken, about 1.6 kg"}, {Position: 2, Amount: fp(2), Unit: "tbsp", Item: "olive oil", RawText: "2 tbsp olive oil"}}
	r2.Steps = []domain.Step{{Position: 1, Text: "Dry the chicken and rub with oil."}, {Position: 2, Text: "Roast on the rack, breast side up."}}
	r2.Images = []domain.Image{img(r2.ID, 1)}
	r2.Labels = []domain.Label{cat("poultry"), tag("sunday")}
	r2.UpdatedBy, r2.UpdatedByName = "u-bob", "Bob"
	// 3. Hebrew shared recipe by bob, pizza artisan.
	r3 := base("fx-pizza-he", "u-bob", "Bob", "פיצה ביתית עם בזיליקום", "shared", 52)
	r3.Language = "he"
	r3.Stages = []domain.OvenStage{{Position: 1, Mode: "pizza", PizzaPreset: "artisan", DurationSeconds: ip(150), PreheatMinutes: ip(15), Accessories: []string{"pizza_stone"}}}
	r3.Ingredients = []domain.Ingredient{
		{Position: 1, Section: "בצק", Amount: fp(500), Unit: "g", Item: "קמח", RawText: "500 גרם קמח"},
		{Position: 2, Section: "בצק", Amount: fp(2), Unit: "tsp", Item: "שמרים יבשים", RawText: "2 כפיות שמרים יבשים"},
		{Position: 3, Section: "תוספות", Item: "מוצרלה ובזיליקום", RawText: "מוצרלה ובזיליקום"}}
	r3.Steps = []domain.Step{{Position: 1, Text: "מערבבים את הבצק ומתפיחים שעה."}, {Position: 2, Text: "אופים כשתי דקות וחצי.", TimerSeconds: ip(150)}}
	r3.Images = []domain.Image{img(r3.ID, 1)}
	r3.Labels = []domain.Label{cat("pizza")}
	// 4. Ownerless shared: broil.
	r4 := base("fx-ownerless", "", "", "Grandma's broiled eggplant", "shared", 200)
	r4.Language = "und"
	r4.OwnerID = ""
	r4.Stages = []domain.OvenStage{{Position: 1, Mode: "broil", TemperatureC: ip(300), DurationSeconds: ip(900), Accessories: []string{"pro_heat_pan"}}}
	r4.Ingredients = []domain.Ingredient{{Position: 1, Amount: fp(3), Unit: "pcs", Item: "eggplants", RawText: "3 eggplants"}}
	r4.Steps = []domain.Step{{Position: 1, Text: "Char until blistered, turning once.", TimerSeconds: ip(900)}}
	r4.Labels = []domain.Label{cat("vegetables")}
	// 5. Alice private bake without photo, no stages.
	r5 := base("fx-bread", "u-alice", "Alice", "Overnight focaccia", "private", 8)
	r5.Stages = []domain.OvenStage{{Position: 1, Mode: "bake", TemperatureC: ip(230), DurationSeconds: ip(25 * 60), Accessories: []string{"own_pan"}}}
	r5.Ingredients = []domain.Ingredient{{Position: 1, Amount: fp(500), Unit: "g", Item: "bread flour", RawText: "500 g bread flour"}}
	r5.Steps = []domain.Step{{Position: 1, Text: "Mix, rest overnight in the fridge."}}
	r5.Labels = []domain.Label{cat("bread_dough")}
	// 6. Alice private, no oven stages.
	r6 := base("fx-nostage", "u-alice", "Alice", "Tahini cookies", "private", 90)
	r6.Ingredients = []domain.Ingredient{{Position: 1, Amount: fp(200), Unit: "g", Item: "tahini", RawText: "200 g tahini"}}
	r6.Steps = []domain.Step{{Position: 1, Text: "Mix and bake."}}
	r6.Labels = []domain.Label{cat("desserts")}
	// 7. Smoked + dehydrate: dried tomatoes by bob, private.
	r7 := base("fx-bob-private", "u-bob", "Bob", "Dried tomatoes", "private", 12)
	r7.Stages = []domain.OvenStage{{Position: 1, Mode: "dehydrate", TemperatureC: ip(60), DurationSeconds: ip(8 * 3600)}}
	r7.Labels = []domain.Label{cat("snacks_dried")}
	r7.Ingredients = []domain.Ingredient{{Position: 1, Amount: fp(1), Unit: "kg", Item: "cherry tomatoes", RawText: "1 kg cherry tomatoes"}}
	r7.Steps = []domain.Step{{Position: 1, Text: "Halve and dry."}}
	d.Recipes = []*domain.Recipe{r1, r2, r3, r4, r5, r6, r7}

	// Inbox of alice.
	in1 := base("fx-inbox-1", "u-alice", "Alice", "Lamb shoulder with rosemary", "private", 2)
	in1.Status = "inbox"
	in1.SourceURL, in1.SourceName = "https://cooking.example.org/lamb-shoulder", "Cooking Example"
	in1.Ingredients = []domain.Ingredient{{Position: 1, Amount: fp(2), Unit: "kg", Item: "lamb shoulder", RawText: "2 kg lamb shoulder"}}
	in1.Steps = []domain.Step{{Position: 1, Text: "Roast until tender."}}
	in1.Images = []domain.Image{img(in1.ID, 1)}
	in1.Labels = []domain.Label{{ID: 4, Type: "category", Slug: "lamb", Name: "Lamb", NameHe: "טלה", Icon: "🍖", Source: "assistant"}}
	in2 := base("fx-inbox-2", "u-alice", "Alice", "Quick shakshuka", "private", 1)
	in2.Status = "inbox"
	in2.Stages = []domain.OvenStage{{Position: 1, Mode: "bake", TemperatureC: ip(200), DurationSeconds: ip(900)}}
	in2.Ingredients = []domain.Ingredient{{Position: 1, Item: "eggs, tomatoes, peppers", RawText: "eggs, tomatoes, peppers"}}
	in2.Steps = []domain.Step{{Position: 1, Text: "Bake."}}
	d.Inbox = []*domain.Recipe{in1, in2}

	// Cook logs and favourites.
	d.Logs["fx-ribs"] = []domain.CookLog{
		{ID: 2, RecipeID: "fx-ribs", UserID: "u-alice", AuthorName: "Alice", CookedAt: ago(30), Note: "Even better with a longer rest.", Rating: ip(5)},
		{ID: 1, RecipeID: "fx-ribs", UserID: "u-alice", AuthorName: "Alice", CookedAt: ago(24 * 20), Rating: ip(4)},
	}
	im := img("fx-ribs", 9)
	im.Kind = "cook_log"
	d.Logs["fx-chicken"] = []domain.CookLog{
		{ID: 3, RecipeID: "fx-chicken", UserID: "u-bob", AuthorName: "Bob", CookedAt: ago(70), Note: "Family loved it.", Rating: ip(5), Image: &im},
		{ID: 4, RecipeID: "fx-chicken", UserID: "u-alice", AuthorName: "Alice", CookedAt: ago(100), Rating: ip(4)},
	}
	d.Favs["u-alice"]["fx-chicken"] = true
	d.Favs["u-alice"]["fx-pizza-he"] = true
	d.Favs["u-bob"]["fx-chicken"] = true
	exp := now.Add(90 * 24 * time.Hour)
	used := now.Add(-3 * time.Hour)
	d.Tokens = []domain.APIToken{
		{ID: "tok-1", UserID: "u-alice", Name: "Home assistant", Prefix: "Ab3dEf9x", Scopes: []string{"inbox:write", "recipes:read"}, CreatedAt: now.Add(-30 * 24 * time.Hour), LastUsedAt: &used, ExpiresAt: &exp},
		{ID: "tok-2", UserID: "u-alice", Name: "Old laptop", Prefix: "Zq81LmNo", Scopes: []string{"inbox:write"}, CreatedAt: now.Add(-200 * 24 * time.Hour), RevokedAt: &used},
	}
	return d
}

// Recipe finds a recipe (active or inbox) by id.
func (d *Data) Recipe(id string) *domain.Recipe {
	for _, r := range d.Recipes {
		if r.ID == id {
			return r
		}
	}
	for _, r := range d.Inbox {
		if r.ID == id {
			return r
		}
	}
	return nil
}
