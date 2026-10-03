package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Title is a search result from a metadata provider.
type Title struct {
	MediaType string  `json:"media_type"` // "movie" or "tv"
	TMDBID    int     `json:"tmdb_id"`
	IMDBID    string  `json:"imdb_id,omitempty"`
	Title     string  `json:"title"`
	Year      int     `json:"year"`
	Overview  string  `json:"overview"`
	PosterURL string  `json:"poster_url"`
	Rating    float64 `json:"rating"`
	// Status is the queue status if this title has already been requested.
	Status string `json:"status,omitempty"`
}

type SearchResult struct {
	Results    []Title `json:"results"`
	Page       int     `json:"page"`
	TotalPages int     `json:"total_pages"`
}

// Provider looks up movie/show metadata.
type Provider interface {
	Name() string
	// Search finds titles; an empty query returns trending/popular titles.
	// mediaType is "movie", "tv" or "" for both.
	Search(ctx context.Context, query, mediaType string, page int) (*SearchResult, error)
	// Details fetches a single title, including its IMDb ID when known.
	Details(ctx context.Context, mediaType string, id int) (*Title, error)
}

var ErrNotFound = errors.New("not found")

// TMDB implements Provider using The Movie Database API
// (free key: https://www.themoviedb.org/settings/api).
type TMDB struct {
	key     string
	baseURL string
	client  *http.Client
}

func NewTMDB(key string) *TMDB {
	return &TMDB{key: key, baseURL: "https://api.themoviedb.org/3", client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *TMDB) Name() string { return "tmdb" }

const tmdbImageBase = "https://image.tmdb.org/t/p/w500"

type tmdbItem struct {
	ID           int     `json:"id"`
	MediaType    string  `json:"media_type"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	VoteAverage  float64 `json:"vote_average"`
	IMDBID       string  `json:"imdb_id"`
	ExternalIDs  struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

func (it tmdbItem) toTitle(mediaType string) Title {
	if it.MediaType != "" {
		mediaType = it.MediaType
	}
	t := Title{MediaType: mediaType, TMDBID: it.ID, Overview: it.Overview, Rating: it.VoteAverage}
	date := it.ReleaseDate
	t.Title = it.Title
	if mediaType == "tv" {
		t.Title, date = it.Name, it.FirstAirDate
	}
	if len(date) >= 4 {
		t.Year, _ = strconv.Atoi(date[:4])
	}
	if it.PosterPath != "" {
		t.PosterURL = tmdbImageBase + it.PosterPath
	}
	t.IMDBID = it.IMDBID
	if t.IMDBID == "" {
		t.IMDBID = it.ExternalIDs.IMDBID
	}
	return t
}

func (t *TMDB) get(ctx context.Context, path string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+path, nil)
	if err != nil {
		return err
	}
	// TMDB issues both a short v3 "API key" and a long v4 "read access token"
	// (a JWT); accept either.
	if strings.HasPrefix(t.key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+t.key)
	} else {
		params.Set("api_key", t.key)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("Accept", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (t *TMDB) Search(ctx context.Context, query, mediaType string, page int) (*SearchResult, error) {
	if page < 1 {
		page = 1
	}
	params := url.Values{"page": {strconv.Itoa(page)}, "include_adult": {"false"}}
	var path string
	switch {
	case query == "" && mediaType == "":
		path = "/trending/all/week"
	case query == "":
		path = "/trending/" + mediaType + "/week"
	case mediaType == "":
		path = "/search/multi"
	default:
		path = "/search/" + mediaType
	}
	if query != "" {
		params.Set("query", query)
	}
	var body struct {
		Page       int        `json:"page"`
		TotalPages int        `json:"total_pages"`
		Results    []tmdbItem `json:"results"`
	}
	if err := t.get(ctx, path, params, &body); err != nil {
		return nil, err
	}
	res := &SearchResult{Page: body.Page, TotalPages: body.TotalPages, Results: []Title{}}
	for _, it := range body.Results {
		title := it.toTitle(mediaType)
		if title.MediaType != "movie" && title.MediaType != "tv" { // multi search also returns people
			continue
		}
		res.Results = append(res.Results, title)
	}
	return res, nil
}

func (t *TMDB) Details(ctx context.Context, mediaType string, id int) (*Title, error) {
	var it tmdbItem
	params := url.Values{}
	if mediaType == "tv" {
		params.Set("append_to_response", "external_ids")
	}
	if err := t.get(ctx, fmt.Sprintf("/%s/%d", mediaType, id), params, &it); err != nil {
		return nil, err
	}
	title := it.toTitle(mediaType)
	return &title, nil
}
