// Package tmdb wraps episode metadata pulled from The Movie Database. The
// binary ships with a pre-fetched cache embedded via go:embed (run
// `--prefetch-tmdb` once with a TMDB API key to populate it). At runtime
// the browse UI reads exclusively from the embedded cache — zero network
// calls on the Pi after deployment.
package tmdb

// Seinfeld's show ID on TMDB. Hardcoded because this is a single-show
// appliance — the v1 product theme is "Seinfeld" and only "Seinfeld".
const SeinfeldShowID = 1400

// Show is the top-level catalog entry.
type Show struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Overview     string   `json:"overview"`
	FirstAirDate string   `json:"first_air_date"`
	NumSeasons   int      `json:"number_of_seasons"`
	NumEpisodes  int      `json:"number_of_episodes"`
	PosterPath   string   `json:"poster_path"`
	BackdropPath string   `json:"backdrop_path"`
	Genres       []Genre  `json:"genres"`
}

// Genre is one of the show's tagged genres.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Season is metadata for one season — includes the full episode list.
type Season struct {
	ID           int       `json:"id"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	SeasonNumber int       `json:"season_number"`
	AirDate      string    `json:"air_date"`
	PosterPath   string    `json:"poster_path"`
	Episodes     []Episode `json:"episodes"`
}

// Episode is one episode record.
type Episode struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	EpisodeNumber int    `json:"episode_number"`
	SeasonNumber  int    `json:"season_number"`
	AirDate       string `json:"air_date"`
	StillPath     string `json:"still_path"`
	Runtime       int    `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
}
