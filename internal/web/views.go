package web

import "time"

// Layout is embedded in every page view model (contract 4.3).
type Layout struct {
	Lang, Dir       string // "en"|"he", "ltr"|"rtl"
	Title           string // localized page title ("" = app name only)
	User            *UserView
	InboxCount      int
	Flash           *FlashView
	CSRFOrigin      string
	AssetVersion    string
	ActiveNav       string // home|search|add|inbox|menu|""
	Path            string // current path+query (return_to of the language form)
	OtherLang       string
	OtherLangNative string
	DevMode         bool
	// ClientI18n (additive to the contract) holds every js.* and oven.mode.* string of the
	// current language; layout.html renders it as <script type="application/json" id="client-i18n">.
	ClientI18n map[string]string
}

func (l *Layout) layout() *Layout { return l }

// layouter lets the renderer fill Layout fields on any page view model.
type layouter interface{ layout() *Layout }

type UserView struct {
	ID, DisplayName string
	IsAdmin         bool
}
type FlashView struct{ Kind, Text string }

type PermFlags struct {
	IsOwner, CanEdit, CanDelete, CanChangeVisibility, CanCopy, ShowCopy bool
	CanFavourite, CanLogCook, IsFavourite                               bool
}

type ImageView struct {
	ID, ThumbURL, FullURL, Caption string
	Width, Height, Position        int
	IsCover                        bool
	RecipeID                       string // used by image_tile (additive)
}
type IngredientView struct {
	Section    string
	Amount     *float64
	Unit, Item string
	RawText    string
	AmountText string // number text after unit conversion
	UnitKey    string // unit key after conversion (g->kg, ml->l)
}
type StepView struct {
	Position     int
	Text         string
	TimerSeconds int
}
type CategoryRef struct{ Slug, Name, Icon string }
type CategoryOption struct {
	Slug, Name, Icon string
	Selected         bool
}

type OvenStageView struct {
	Position                                                                                        int
	Mode, Preset, PelletBlend                                                                       string
	PelletNote                                                                                      string
	TemperatureC, Temperature2C, DurationSeconds, Duration2Seconds, PreheatMinutes, TargetCoreTempC *int
	TotalSeconds                                                                                    int
	IsGourmet, Woodfire, Preheat                                                                    bool
	FrameLevel                                                                                      string
	ShowFrameProminent                                                                              bool
	Accessories                                                                                     []string
	Notes                                                                                           string
	OilWarning                                                                                      bool
	SmokerIgnition                                                                                  bool
	PreAlert                                                                                        bool
}

type RecipeBody struct {
	ID, Title, Description, Notes     string
	Language, Lang, Dir               string
	Servings, PrepMinutes             *int
	SourceURL, SourceName, SourceHost string
	Visibility                        string
	Stages                            []OvenStageView
	Ingredients                       []IngredientView
	Steps                             []StepView
	Images                            []ImageView
	Categories                        []CategoryRef
	Tags                              []string
	OwnerName                         string
}

type RecipeCardView struct {
	ID, Title, Lang, Dir, ThumbURL string
	PrimaryMode                    string
	PrimaryTempC                   *int
	Visibility                     string
	UsesWoodfire, IsFavourite      bool
	TotalSeconds                   int
	At                             *time.Time
}

type CookSummary struct {
	Count       int
	LastAt      *time.Time
	AvgRating   *float64
	RatingCount int
}
type CookLogView struct {
	ID                                int64
	AuthorName                        string
	At                                time.Time
	Rating                            *int
	Note, PhotoThumbURL, PhotoFullURL string
	CanDelete                         bool
}
type LastChanged struct {
	At     time.Time
	ByName string
}
type VisibilityView struct {
	RecipeID          string
	Shared, CanChange bool
	OwnerName         string
}
type FavouriteView struct {
	RecipeID    string
	IsFavourite bool
}
type DeleteDialogView struct {
	ID, Title, Action string
	Shared            bool
}

// ---- pages ----

type HomeView struct {
	Layout
	Tiles              []CategoryRef
	Recent, Favourites []RecipeCardView
}

type ModeOption struct {
	Key      string
	Selected bool
}
type SortOption struct {
	Key      string
	Selected bool
}
type ActiveFilter struct{ Label, RemoveURL string }

type RecipesListView struct {
	Layout
	Q, Scope, Sort string
	Smoked, Fav    bool
	MaxTime        int
	Categories     []CategoryOption
	Modes          []ModeOption
	SelectedTags   []string
	SortOptions    []SortOption
	Active         []ActiveFilter
	HasFilters     bool
	Results        ResultsView
}
type ResultsView struct {
	Cards      []RecipeCardView
	NextURL    string
	Q          string
	HasFilters bool
}

type RecipeView struct {
	Layout
	RecipeBody
	PermFlags
	Visibility     VisibilityView
	FavouriteState FavouriteView
	Cook           CookSummary
	Logs           CookLogSectionView
	LastChanged    *LastChanged
	ShareURL       string
	Delete         DeleteDialogView
	Editable       bool
	MaxNote        int
}
type CookLogSectionView struct {
	RecipeID    string
	Summary     CookSummary
	Logs        []CookLogView
	CanLogCook  bool
	PhotoFailed bool
	MaxNote     int
}

type CookView struct {
	Layout
	RecipeBody
	Config CookConfig
}
type CookStageConfig struct {
	Position      int    `json:"position"`
	Label         string `json:"label"`
	TotalSeconds  int    `json:"totalSeconds"`
	Stage1Seconds int    `json:"stage1Seconds"`
	IsGourmet     bool   `json:"isGourmet"`
	PreAlert      bool   `json:"preAlert"`
	Stage2Notice  string `json:"stage2Notice"`
}

// CookConfig is rendered into <script id="cook-config"> (camelCase JSON keys).
type CookConfig struct {
	RecipeID string            `json:"recipeId"`
	UserID   string            `json:"userId"`
	Title    string            `json:"title"`
	Servings *int              `json:"servings"`
	Stages   []CookStageConfig `json:"stages"`
}

type InboxCardView struct {
	ID, Title, ThumbURL, SourceHost, Lang, Dir string
	ReceivedAt                                 time.Time
}
type InboxListView struct {
	Layout
	Items []InboxCardView
}
type NextRef struct{ ID string }
type InboxItemView struct {
	Layout
	RecipeBody
	MissingOven bool
	Next        *NextRef
}

type SettingsView struct {
	Layout
	Version, Commit, BuildDate string
}
type TokenView struct {
	ID, Name, Prefix      string
	Scopes                []string
	CreatedAt             time.Time
	LastUsedAt, ExpiresAt *time.Time
	Status                string
}
type TokensView struct {
	Layout
	Tokens        []TokenView
	NewToken      string
	Brief         string // the user's "what should your assistant look for" text
	ScopeOptions  []string
	ExpiryOptions []int
	FormErrors    map[string]string
}
type AdminCategory struct {
	ID                       int64
	Slug, Name, NameHe, Icon string
	SortOrder, RecipeCount   int
}
type AdminCategoriesView struct {
	Layout
	Items      []AdminCategory
	FormErrors map[string]string
}
type SigninView struct {
	Layout
	LoginURL string
}
type LoggedOutView struct{ Layout }
type ErrorView struct {
	Layout
	Status    int
	Code      string
	RequestID string
	BackURL   string
}

// ---- editor ----

type ErrorItem struct{ FieldID, Message string }
type Option struct{ Key, Label, Dial string }

// OvenSpecLabels are the localized option lists for the editor selects.
type OvenSpecLabels struct {
	Modes       []Option
	Presets     []Option
	Blends      []Option
	Accessories []Option
}

type EditorConfig struct {
	Mode                   string `json:"mode"`
	RecipeID               string `json:"recipeId"`
	UserID                 string `json:"userId"`
	DraftKey               string `json:"draftKey"`
	Version                int    `json:"version"`
	Action                 string `json:"action"`
	SigninURL              string `json:"signinUrl"`
	SuggestURL             string `json:"suggestUrl"`
	UploadURL              string `json:"uploadUrl"`
	OrderURL               string `json:"orderUrl"`
	DeleteImageURLTemplate string `json:"deleteImageUrlTemplate"`
	MaxImages              int    `json:"maxImages"`
	MaxImageBytes          int    `json:"maxImageBytes"`
	MaxStages              int    `json:"maxStages"`
}

type StageForm struct {
	Idx                                                             string
	Mode, PizzaPreset, TemperatureC, DurationSeconds, Temperature2C string
	Duration2Seconds, PreheatMinutes                                string
	Woodfire                                                        bool
	PelletBlend, PelletNote                                         string
	Accessories                                                     []string
	TargetCoreTempC, Notes                                          string
}
type RecipeForm struct {
	Version                                      int
	Title, Description, Notes, Language          string
	Servings, PrepMinutes, SourceURL, SourceName string
	CategorySlugs                                []string
	Tags                                         string
	Ingredients, Steps                           string
	Visibility                                   string
	Stages                                       []StageForm
}
type EditorView struct {
	Layout
	Mode          string // new|edit|inbox
	Action        string
	SubmitKey     string
	Form          RecipeForm
	AllCategories []CategoryOption
	Images        []ImageView
	Errors        map[string][]string
	Summary       []ErrorItem
	Warnings      map[string][]string
	Config        EditorConfig
	OvenSpec      any
	Spec          OvenSpecLabels
	CanDelete     bool
	Delete        DeleteDialogView
	StageTemplate StageForm
}

// Small data carriers for fragments.
type UploadError struct{ Message string }
type ConflictView struct{ RecipeURL string }

// Layout and RecipeBody both have Title, Lang and Dir (page/UI vs recipe content), which would make `.Title`
// ambiguous in templates for the three types that embed both. These methods (depth 0) resolve the
// ambiguity: on RecipeView, CookView and InboxItemView, `.Title`, `.Lang`, `.Dir` are the RECIPE's values;
// layout.html must read the page/UI values as `.Layout.Title`, `.Layout.Lang`, `.Layout.Dir` (valid for every
// page view model).
func (v *RecipeView) Title() string    { return v.RecipeBody.Title }
func (v *RecipeView) Lang() string     { return v.RecipeBody.Lang }
func (v *RecipeView) Dir() string      { return v.RecipeBody.Dir }
func (v *CookView) Title() string      { return v.RecipeBody.Title }
func (v *CookView) Lang() string       { return v.RecipeBody.Lang }
func (v *CookView) Dir() string        { return v.RecipeBody.Dir }
func (v *InboxItemView) Title() string { return v.RecipeBody.Title }
func (v *InboxItemView) Lang() string  { return v.RecipeBody.Lang }
func (v *InboxItemView) Dir() string   { return v.RecipeBody.Dir }
