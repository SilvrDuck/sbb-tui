// Package api wraps the transport.opendata.ch HTTP endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/necrom4/sbb-tui/model"
)

type locationsResponse struct {
	Stations []struct {
		Name string `json:"name"`
	} `json:"stations"`
}

// Location is one /v1/locations row including the fields the legacy
// FetchLocations dropped: the numeric id (UIC for stations, empty for
// addresses/POIs) and the location type ("station", "address", "poi").
// Used by the fuzzy popover to back-fill rows missing from the local
// index — most importantly addresses, but also recently-opened stations
// not yet in the embedded snapshot.
type Location struct {
	ID   string
	Name string
	Type string
}

type locationsFullResponse struct {
	Stations []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"stations"`
}

type connectionsResponse struct {
	Connections []model.Connection `json:"connections"`
}

// FetchLocations returns station name suggestions matching query.
func FetchLocations(query string) ([]string, error) {
	apiURL := "https://transport.opendata.ch/v1/locations?type=station&query=" + url.QueryEscape(query)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("fetching locations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching locations: API returned %s", resp.Status)
	}

	var result locationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("fetching locations: decoding response: %w", err)
	}

	names := make([]string, 0, len(result.Stations))
	for _, s := range result.Stations {
		if s.Name != "" {
			names = append(names, s.Name)
		}
	}
	return names, nil
}

// SearchLocations queries /v1/locations for stations and addresses in
// parallel and returns the combined deduped rows. Stations come back from
// the default endpoint with a numeric id (UIC); addresses require an
// explicit ?type=address filter and come back with id=null — we tag those
// as "address" in Location.Type. The fuzzy popover uses this to back-fill
// anything the local index doesn't cover, most importantly street-level
// addresses like "11 route de bardonnex" which the connections endpoint
// then resolves to the nearest stop.
func SearchLocations(query string) ([]Location, error) {
	// Address-endpoint quirk: the HAFAS geocoder behind /v1/locations
	// expects the house number to *trail* the street name. With the
	// number leading ("11 route de bardonnex") it returns 0 rows; with
	// the number trailing ("route de bardonnex 11") the exact address
	// comes back as the first match. We rewrite the query in that order
	// for the address endpoint only — stations still see the original.
	addressQuery := moveLeadingHouseNumberToEnd(query)

	stationsCh := make(chan searchResult, 1)
	addressesCh := make(chan searchResult, 1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		locs, err := fetchLocations(query, "station")
		stationsCh <- searchResult{locs: locs, err: err}
	}()
	go func() {
		defer wg.Done()
		locs, err := fetchLocations(addressQuery, "address")
		addressesCh <- searchResult{locs: locs, err: err}
	}()
	wg.Wait()
	close(stationsCh)
	close(addressesCh)
	stations := <-stationsCh
	addresses := <-addressesCh

	// Either both endpoints fail → bubble up. One failing is non-fatal:
	// addresses are a luxury, stations the load-bearing case.
	if stations.err != nil && addresses.err != nil {
		return nil, stations.err
	}

	out := make([]Location, 0, len(stations.locs)+len(addresses.locs))
	out = append(out, stations.locs...)
	seenNames := make(map[string]bool, len(stations.locs))
	for _, l := range stations.locs {
		seenNames[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}
	for _, l := range addresses.locs {
		key := strings.ToLower(strings.TrimSpace(l.Name))
		if seenNames[key] {
			continue
		}
		l.Type = "address"
		out = append(out, l)
	}
	return out, nil
}

// searchResult bundles a one-endpoint result for the SearchLocations fan-in.
type searchResult struct {
	locs []Location
	err  error
}

// leadingHouseNumber matches "11 route de bardonnex" but not "11" alone
// (would leave an empty street name) and not "Salzweg 11" (which the
// geocoder already handles).
var leadingHouseNumber = regexp.MustCompile(`^\s*(\d+)\s+(\S.*)$`)

// moveLeadingHouseNumberToEnd rewrites "11 route de bardonnex" as
// "route de bardonnex 11" so the HAFAS geocoder behind type=address
// matches the exact house number. Returns the original query when the
// pattern doesn't apply.
func moveLeadingHouseNumberToEnd(query string) string {
	if m := leadingHouseNumber.FindStringSubmatch(query); m != nil {
		return m[2] + " " + m[1]
	}
	return query
}

// fetchLocations issues one /v1/locations call constrained to the given
// type ("station" or "address") and returns the decoded rows.
func fetchLocations(query, locType string) ([]Location, error) {
	apiURL := "https://transport.opendata.ch/v1/locations?type=" + url.QueryEscape(locType) +
		"&query=" + url.QueryEscape(query)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("searching %s locations: %w", locType, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searching %s locations: API returned %s", locType, resp.Status)
	}

	var result locationsFullResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("searching %s locations: decoding response: %w", locType, err)
	}

	out := make([]Location, 0, len(result.Stations))
	for _, s := range result.Stations {
		if s.Name == "" {
			continue
		}
		out = append(out, Location{ID: s.ID, Name: strings.TrimSpace(s.Name), Type: s.Type})
	}
	return out, nil
}

// FetchConnections returns up to `limit` connections between two stations.
func FetchConnections(from, to, date, timeStr string, isArrivalTime bool, limit int) ([]model.Connection, error) {
	parts := []string{
		fmt.Sprintf("from=%s", url.QueryEscape(from)),
		fmt.Sprintf("to=%s", url.QueryEscape(to)),
	}
	if date != "" {
		parts = append(parts, fmt.Sprintf("date=%s", url.QueryEscape(date)))
	}
	if timeStr != "" {
		parts = append(parts, fmt.Sprintf("time=%s", url.QueryEscape(timeStr)))
	}

	isArrival := "0"
	if isArrivalTime {
		isArrival = "1"
	}
	parts = append(
		parts,
		fmt.Sprintf("isArrivalTime=%s", isArrival),
		fmt.Sprintf("limit=%v", limit),
	)

	apiURL := "https://transport.opendata.ch/v1/connections?" + strings.Join(parts, "&")

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("fetching connections: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching connections: API returned %s", resp.Status)
	}

	var result connectionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("fetching connections: decoding response: %w", err)
	}

	return result.Connections, nil
}
