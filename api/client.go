// Package api wraps the transport.opendata.ch HTTP endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/necrom4/sbb-tui/model"
)

type locationsResponse struct {
	Stations []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	} `json:"stations"`
}

// Location is a structured station hit returned by the SBB locations endpoint.
// ID is the UIC code that joins back to the local Service Points dataset.
type Location struct {
	UIC  string
	Name string
	Icon string
}

type connectionsResponse struct {
	Connections []model.Connection `json:"connections"`
}

// FetchLocations returns station name suggestions matching query. Kept for
// the legacy --fuzzy=false path; the fuzzy popover uses FetchLocationsWithIDs.
func FetchLocations(query string) ([]string, error) {
	locs, err := FetchLocationsWithIDs(query)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(locs))
	for _, l := range locs {
		if l.Name != "" {
			names = append(names, l.Name)
		}
	}
	return names, nil
}

// FetchLocationsWithIDs returns structured station hits with UIC + icon, so
// the caller can dedup-by-UIC against the local station index.
func FetchLocationsWithIDs(query string) ([]Location, error) {
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

	locs := make([]Location, 0, len(result.Stations))
	for _, s := range result.Stations {
		if s.Name == "" {
			continue
		}
		locs = append(locs, Location{UIC: s.ID, Name: s.Name, Icon: s.Icon})
	}
	return locs, nil
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
