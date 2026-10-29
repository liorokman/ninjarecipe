// Package domain holds plain structs, enums and typed errors shared by all layers.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// Clock lets services and jobs be tested with a controlled "now".
type Clock interface{ Now() time.Time }

// SystemClock is the real clock (UTC).
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// ---- enums (25 §3) ----

const (
	ModePizza        = "pizza"
	ModeGourmetRoast = "gourmet_roast"
	ModeMaxRoast     = "max_roast"
	ModeSmoker       = "smoker"
	ModeBake         = "bake"
	ModeBroil        = "broil"
	ModeDehydrate    = "dehydrate"
	ModeKeepWarm     = "keep_warm"
)

var (
	OvenModes    = []string{ModePizza, ModeGourmetRoast, ModeMaxRoast, ModeSmoker, ModeBake, ModeBroil, ModeDehydrate, ModeKeepWarm}
	PizzaPresets = []string{"artisan", "thin_crust", "new_york", "chicago", "calzone", "custom"}
	PelletBlends = []string{"all_purpose", "robust", "other"}
	Accessories  = []string{"pizza_stone", "pro_heat_pan", "roast_rack", "own_pan"}
	Units        = []string{"g", "kg", "ml", "l", "tsp", "tbsp", "pcs", "pinch"}
)

const (
	StatusInbox  = "inbox"
	StatusActive = "active"

	VisPrivate = "private"
	VisShared  = "shared"

	LangEN  = "en"
	LangHE  = "he"
	LangUnd = "und"
)

// Limits from 20 §8.
const (
	MaxTitle        = 200
	MaxDescription  = 2000
	MaxNotes        = 8000
	MaxIngredient   = 300
	MaxStep         = 4000
	MaxIngredients  = 100
	MaxSteps        = 100
	MaxStages       = 10
	MaxStageNotes   = 500
	MaxPelletNote   = 60
	MaxCategories   = 5
	MaxTags         = 20
	MaxTag          = 40
	MaxCookLogNote  = 2000
	MaxSourceURL    = 2000
	MaxSourceName   = 100
	MaxActiveTokens = 10
)

func Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---- entities ----

type User struct {
	ID          string
	OIDCIssuer  string
	OIDCSubject string
	Email       string
	DisplayName string
	IsAdmin     bool
	UILanguage  string // "" = not chosen
	LastLoginAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Session struct {
	IDHash     string
	UserID     string
	IDToken    string
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

type OIDCState struct {
	State, Verifier, Nonce, ReturnTo string
	ExpiresAt                        time.Time
}

type APIToken struct {
	ID, UserID, Name, Hash, Prefix   string
	Scopes                           []string
	LastUsedAt, ExpiresAt, RevokedAt *time.Time
	CreatedAt                        time.Time
}

// Status of a token for display.
func (t APIToken) Status(now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case t.ExpiresAt != nil && !t.ExpiresAt.After(now):
		return "expired"
	}
	return "active"
}

func (t APIToken) HasScope(s string) bool { return Contains(t.Scopes, s) }

const (
	ScopeInboxWrite  = "inbox:write"
	ScopeRecipesRead = "recipes:read"
)

type Label struct {
	ID        int64
	Type      string // category|tag
	Slug      string
	Name      string
	NameHe    string
	Icon      string
	SortOrder int
	Source    string // recipe_labels.source when attached to a recipe
}

// LocalName returns the category name for the language.
func (l Label) LocalName(lang string) string {
	if l.Type == "category" && lang == LangHE && l.NameHe != "" {
		return l.NameHe
	}
	return l.Name
}

type OvenStage struct {
	Position         int
	Mode             string
	PizzaPreset      string
	TemperatureC     *int
	DurationSeconds  *int
	Temperature2C    *int
	Duration2Seconds *int
	PreheatMinutes   *int
	Woodfire         bool
	PelletBlend      string
	PelletNote       string
	Accessories      []string
	TargetCoreTempC  *int
	Notes            string
}

type Ingredient struct {
	Position int
	Section  string
	Amount   *float64
	Unit     string
	Item     string
	RawText  string
}

type Step struct {
	Position     int
	Text         string
	TimerSeconds *int
}

type Image struct {
	ID        string
	RecipeID  string
	Key       string
	Kind      string // recipe|cook_log
	Position  int    // 0 for cook_log
	Width     int
	Height    int
	Bytes     int64
	Caption   string
	SourceURL string
	CreatedAt time.Time
}

type Recipe struct {
	ID            string
	OwnerID       string // "" = ownerless
	Status        string
	Visibility    string
	Title         string
	Description   string
	Notes         string
	Language      string
	Servings      *int
	PrepMinutes   *int
	SourceURL     string
	SourceName    string
	ImportKey     string
	Version       int
	CategorizedAt *time.Time
	CreatedBy     string
	UpdatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Ingredients []Ingredient
	Steps       []Step
	Stages      []OvenStage
	Images      []Image // kind=recipe, ordered by position
	Labels      []Label // categories and tags with Source

	OwnerName     string // joined
	UpdatedByName string
	CreatedByName string
}

func (r *Recipe) Categories() []Label { return r.byType("category") }
func (r *Recipe) Tags() []Label       { return r.byType("tag") }
func (r *Recipe) byType(t string) []Label {
	var out []Label
	for _, l := range r.Labels {
		if l.Type == t {
			out = append(out, l)
		}
	}
	return out
}

type CookLog struct {
	ID         int64
	RecipeID   string
	UserID     string
	AuthorName string
	CookedAt   time.Time
	Note       string
	Rating     *int
	Image      *Image
}

// ---- errors ----

var (
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("forbidden")
	ErrConflict         = errors.New("version conflict")
	ErrTooLarge         = errors.New("too large")
	ErrUnsupportedMedia = errors.New("unsupported media type")
	ErrBusy             = errors.New("busy")
	ErrLimit            = errors.New("limit reached")
)

// ErrDuplicate: a recipe with the same source key exists.
type ErrDuplicate struct{ ExistingID string }

func (e ErrDuplicate) Error() string { return "duplicate source_url, existing " + e.ExistingID }

// ErrPreviouslyReviewed: the source was already kept or thrown away by this user (40 §4 rule 3).
type ErrPreviouslyReviewed struct {
	Outcome   string // kept|rejected
	DecidedAt time.Time
	Reason    RejectReason
}

func (e ErrPreviouslyReviewed) Error() string { return "source previously reviewed: " + e.Outcome }

// RejectReason is the optional reason given when throwing an inbox item away (30 §5a).
type RejectReason string

const (
	ReasonNotOurTaste    RejectReason = "not_our_taste"
	ReasonTooComplicated RejectReason = "too_complicated"
	ReasonNotForOven     RejectReason = "not_for_our_oven"
	ReasonAlreadyHave    RejectReason = "already_have"
	ReasonPoorDetails    RejectReason = "poor_details"
)

// RejectReasons lists the valid codes in display order.
var RejectReasons = []RejectReason{ReasonNotOurTaste, ReasonTooComplicated, ReasonNotForOven, ReasonAlreadyHave, ReasonPoorDetails}

// Valid reports whether r is one of RejectReasons.
func (r RejectReason) Valid() bool {
	for _, x := range RejectReasons {
		if r == x {
			return true
		}
	}
	return false
}

const (
	OutcomeKept     = "kept"
	OutcomeRejected = "rejected"

	MaxRejectNote     = 200
	MaxAssistantBrief = 2000
)

// Outcome is one Keep / Throw away decision on an inbox item.
type Outcome struct {
	ID        int64
	ItemID    string
	RecipeID  string // item id when kept and the recipe still exists
	Title     string
	SourceURL string
	SourceKey string
	Outcome   string
	Reason    RejectReason
	Note      string
	Edited    bool
	Shared    bool
	DecidedAt time.Time
}

// OutcomePage is one page of outcomes in decision order.
type OutcomePage struct {
	Items      []Outcome
	NextCursor string
}

// AssistantBrief is the user's "what should your assistant look for" text.
type AssistantBrief struct {
	Text      string
	Language  string
	UpdatedAt *time.Time
}

// Issue is a machine-readable validation message (code + params; no human text).
type Issue struct {
	Field  string         `json:"name"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// ValidationError carries blocking errors and non-blocking warnings.
type ValidationError struct {
	Errors   []Issue
	Warnings []Issue
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("validation failed (%d errors)", len(v.Errors))
}

// RecipeSummary is a recipe row for cards and the API list.
type RecipeSummary struct {
	ID           string
	OwnerID      string
	OwnerName    string
	Status       string
	Visibility   string
	Title        string
	Language     string
	SourceURL    string
	PrimaryMode  string
	PrimaryTempC *int
	TotalSeconds int
	UsesWoodfire bool
	IsFavourite  bool
	CoverKey     string
	CoverFull    bool
	Categories   []string // slugs
	Tags         []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	At           *time.Time // recently cooked/viewed time
	CookCount    int
	LastCookedAt *time.Time
	AvgRating    *float64
}

// CookStats aggregates visible cook logs.
type CookStats struct {
	Count       int
	LastAt      *time.Time
	AvgRating   *float64
	RatingCount int
}

// RecipeInput is the write model for create/update (form and API share it).
type RecipeInput struct {
	Title, Description, Notes, Language string
	Servings, PrepMinutes               *int
	SourceURL, SourceName               string
	Categories                          []string // category slugs
	Tags                                []string // normalized
	Ingredients                         []Ingredient
	Steps                               []Step
	Stages                              []OvenStage
	Visibility                          string // create only
	// Source marks recipe_labels.source ("user" or "assistant").
	LabelSource string
	// FormIssues are parse errors found by the web layer (merged into validation).
	FormIssues []Issue
}

// SearchQuery holds the list/search parameters of 40 §2.3.
type SearchQuery struct {
	Q          string
	Scope      string // all|mine|family
	Categories []string
	Tags       []string
	Modes      []string
	Smoked     bool
	MaxTimeMin int
	Fav        bool
	Sort       string // relevance|updated|title|cooked|rating|recent
	Cursor     string
	Limit      int
	SourceURL  string // matched by source key (40 §4.3, API)
}

// SearchPage is one page of results.
type SearchPage struct {
	Items      []RecipeSummary
	NextCursor string
}

// ErrBadRequest marks malformed input that is not a field validation problem.
var ErrBadRequest = errors.New("bad request")
