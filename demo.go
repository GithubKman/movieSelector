package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"html"
	"net/http"
	"strconv"
	"strings"
)

// DemoProvider is an offline catalog used when no TMDB key is configured, so
// the whole app can be tried out without signing up for anything. Posters are
// generated SVGs served by the app itself.
type DemoProvider struct {
	titles []Title
}

func NewDemoProvider() *DemoProvider {
	d := &DemoProvider{}
	for _, t := range demoCatalog {
		t.PosterURL = fmt.Sprintf("/demo-poster/%s/%d.svg", t.MediaType, t.TMDBID)
		d.titles = append(d.titles, t)
	}
	return d
}

func (d *DemoProvider) Name() string { return "demo" }

const demoPageSize = 20

func (d *DemoProvider) Search(_ context.Context, query, mediaType string, page int) (*SearchResult, error) {
	if page < 1 {
		page = 1
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var matches []Title
	for _, t := range d.titles {
		if mediaType != "" && t.MediaType != mediaType {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(t.Title), q) && !strings.Contains(strings.ToLower(t.Overview), q) {
			continue
		}
		matches = append(matches, t)
	}
	res := &SearchResult{Page: page, TotalPages: (len(matches) + demoPageSize - 1) / demoPageSize, Results: []Title{}}
	start := (page - 1) * demoPageSize
	if start < len(matches) {
		res.Results = matches[start:min(start+demoPageSize, len(matches))]
	}
	return res, nil
}

func (d *DemoProvider) Details(_ context.Context, mediaType string, id int) (*Title, error) {
	for _, t := range d.titles {
		if t.MediaType == mediaType && t.TMDBID == id {
			return &t, nil
		}
	}
	return nil, ErrNotFound
}

func (d *DemoProvider) find(mediaType string, id int) *Title {
	t, _ := d.Details(context.Background(), mediaType, id)
	return t
}

// ServePoster renders a simple gradient poster for a demo title.
func (d *DemoProvider) ServePoster(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(strings.TrimSuffix(r.PathValue("id"), ".svg"))
	t := d.find(r.PathValue("type"), id)
	if t == nil {
		http.NotFound(w, r)
		return
	}
	h := fnv.New32a()
	h.Write([]byte(t.Title))
	hue := h.Sum32() % 360

	// Wrap the title onto lines of ~14 characters.
	var lines []string
	var cur string
	for _, word := range strings.Fields(t.Title) {
		if cur != "" && len(cur)+1+len(word) > 11 {
			lines, cur = append(lines, cur), word
		} else if cur == "" {
			cur = word
		} else {
			cur += " " + word
		}
	}
	lines = append(lines, cur)

	var text strings.Builder
	y := 400 - len(lines)*32
	for _, l := range lines {
		fmt.Fprintf(&text, `<text x="40" y="%d" font-size="56" font-weight="700">%s</text>`, y, html.EscapeString(l))
		y += 64
	}
	kind := "FILM"
	if t.MediaType == "tv" {
		kind = "SERIES"
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 500 750" width="500" height="750">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
<stop offset="0" stop-color="hsl(%d,65%%,45%%)"/><stop offset="1" stop-color="hsl(%d,70%%,12%%)"/>
</linearGradient></defs>
<rect width="500" height="750" fill="url(#g)"/>
<circle cx="420" cy="110" r="160" fill="hsl(%d,80%%,70%%)" opacity="0.18"/>
<g fill="#fff" font-family="Georgia, serif">%s</g>
<text x="40" y="690" fill="#fff" opacity="0.75" font-family="sans-serif" font-size="22" letter-spacing="6">%s · %d</text>
</svg>`, hue, (hue+40)%360, (hue+180)%360, text.String(), kind, t.Year)
}

var demoCatalog = []Title{
	{MediaType: "movie", TMDBID: 603, IMDBID: "tt0133093", Title: "The Matrix", Year: 1999, Rating: 8.2, Overview: "A hacker learns that the world he lives in is a simulation and joins a rebellion against the machines that built it."},
	{MediaType: "movie", TMDBID: 27205, IMDBID: "tt1375666", Title: "Inception", Year: 2010, Rating: 8.4, Overview: "A thief who steals secrets through shared dreams is offered one last job: planting an idea instead."},
	{MediaType: "movie", TMDBID: 157336, IMDBID: "tt0816692", Title: "Interstellar", Year: 2014, Rating: 8.4, Overview: "With Earth failing, a team of explorers travels through a wormhole in search of a new home for humanity."},
	{MediaType: "movie", TMDBID: 155, IMDBID: "tt0468569", Title: "The Dark Knight", Year: 2008, Rating: 8.5, Overview: "Batman faces the Joker, a criminal mastermind intent on plunging Gotham into anarchy."},
	{MediaType: "movie", TMDBID: 550, IMDBID: "tt0137523", Title: "Fight Club", Year: 1999, Rating: 8.4, Overview: "An insomniac office worker and a soap salesman form an underground fight club that grows into something more."},
	{MediaType: "movie", TMDBID: 680, IMDBID: "tt0110912", Title: "Pulp Fiction", Year: 1994, Rating: 8.5, Overview: "Interlocking stories of hitmen, a boxer and a gangster's wife in Los Angeles."},
	{MediaType: "movie", TMDBID: 238, IMDBID: "tt0068646", Title: "The Godfather", Year: 1972, Rating: 8.7, Overview: "The aging patriarch of a crime dynasty transfers control to his reluctant son."},
	{MediaType: "movie", TMDBID: 129, IMDBID: "tt0245429", Title: "Spirited Away", Year: 2001, Rating: 8.5, Overview: "A young girl wanders into a world of spirits and must work in a bathhouse to free her parents."},
	{MediaType: "movie", TMDBID: 496243, IMDBID: "tt6751668", Title: "Parasite", Year: 2019, Rating: 8.5, Overview: "A poor family schemes its way into employment with a wealthy household, with unexpected results."},
	{MediaType: "movie", TMDBID: 278, IMDBID: "tt0111161", Title: "The Shawshank Redemption", Year: 1994, Rating: 8.7, Overview: "A banker sentenced to life in prison forms a lasting friendship and quietly plans for freedom."},
	{MediaType: "movie", TMDBID: 13, IMDBID: "tt0109830", Title: "Forrest Gump", Year: 1994, Rating: 8.5, Overview: "A kind-hearted man unwittingly finds himself at the centre of decades of American history."},
	{MediaType: "movie", TMDBID: 11, IMDBID: "tt0076759", Title: "Star Wars", Year: 1977, Rating: 8.2, Overview: "A farm boy joins a princess, a smuggler and an old knight to fight a galactic empire."},
	{MediaType: "movie", TMDBID: 329, IMDBID: "tt0107290", Title: "Jurassic Park", Year: 1993, Rating: 7.9, Overview: "A theme park of cloned dinosaurs goes badly wrong during a preview tour."},
	{MediaType: "movie", TMDBID: 78, IMDBID: "tt0083658", Title: "Blade Runner", Year: 1982, Rating: 7.9, Overview: "A detective in a rain-soaked future Los Angeles hunts down rogue artificial humans."},
	{MediaType: "movie", TMDBID: 348, IMDBID: "tt0078748", Title: "Alien", Year: 1979, Rating: 8.1, Overview: "The crew of a commercial spaceship is stalked by a deadly creature they brought aboard."},
	{MediaType: "movie", TMDBID: 105, IMDBID: "tt0088763", Title: "Back to the Future", Year: 1985, Rating: 8.3, Overview: "A teenager is accidentally sent thirty years into the past in a time-travelling DeLorean."},
	{MediaType: "movie", TMDBID: 862, IMDBID: "tt0114709", Title: "Toy Story", Year: 1995, Rating: 8.0, Overview: "A cowboy doll feels threatened when a flashy space ranger becomes his owner's favourite toy."},
	{MediaType: "movie", TMDBID: 120, IMDBID: "tt0120737", Title: "The Lord of the Rings: The Fellowship of the Ring", Year: 2001, Rating: 8.4, Overview: "A hobbit sets out with eight companions to destroy a ring of terrible power."},
	{MediaType: "movie", TMDBID: 98, IMDBID: "tt0172495", Title: "Gladiator", Year: 2000, Rating: 8.2, Overview: "A betrayed Roman general fights his way through the arena to seek revenge."},
	{MediaType: "movie", TMDBID: 597, IMDBID: "tt0120338", Title: "Titanic", Year: 1997, Rating: 7.9, Overview: "Two young people from different social classes fall in love aboard the doomed ocean liner."},
	{MediaType: "movie", TMDBID: 19995, IMDBID: "tt0499549", Title: "Avatar", Year: 2009, Rating: 7.6, Overview: "A paraplegic marine on an alien moon is torn between his orders and the world he comes to love."},
	{MediaType: "movie", TMDBID: 76341, IMDBID: "tt1392190", Title: "Mad Max: Fury Road", Year: 2015, Rating: 7.6, Overview: "In a desert wasteland, a drifter and a rebel warrior flee a tyrant across the sand."},
	{MediaType: "movie", TMDBID: 438631, IMDBID: "tt1160419", Title: "Dune", Year: 2021, Rating: 7.8, Overview: "The heir of a noble family is thrust into a war over the most valuable resource in the universe."},
	{MediaType: "movie", TMDBID: 545611, IMDBID: "tt6710474", Title: "Everything Everywhere All at Once", Year: 2022, Rating: 7.8, Overview: "A laundromat owner discovers she must connect with parallel-universe versions of herself."},
	{MediaType: "movie", TMDBID: 329865, IMDBID: "tt2543164", Title: "Arrival", Year: 2016, Rating: 7.6, Overview: "A linguist is recruited to communicate with mysterious visitors after alien ships land on Earth."},
	{MediaType: "tv", TMDBID: 1396, IMDBID: "tt0903747", Title: "Breaking Bad", Year: 2008, Rating: 8.9, Overview: "A chemistry teacher diagnosed with cancer turns to manufacturing drugs to secure his family's future."},
	{MediaType: "tv", TMDBID: 1399, IMDBID: "tt0944947", Title: "Game of Thrones", Year: 2011, Rating: 8.5, Overview: "Noble families wage war for control of the Iron Throne while an ancient threat returns."},
	{MediaType: "tv", TMDBID: 2316, IMDBID: "tt0386676", Title: "The Office", Year: 2005, Rating: 8.6, Overview: "A mockumentary about the everyday lives of employees at a paper company branch office."},
	{MediaType: "tv", TMDBID: 66732, IMDBID: "tt4574334", Title: "Stranger Things", Year: 2016, Rating: 8.6, Overview: "When a boy vanishes, a small town uncovers secret experiments and supernatural forces."},
	{MediaType: "tv", TMDBID: 82856, IMDBID: "tt8111088", Title: "The Mandalorian", Year: 2019, Rating: 8.4, Overview: "A lone bounty hunter makes his way through the outer reaches of the galaxy."},
}
