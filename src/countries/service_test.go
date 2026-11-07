package countries

import (
	"encoding/json"
	"math"
	"testing"
)

// fixtureJSON is a small, hand-crafted dataset with known coordinates so
// distance/nearest calculations can be checked against expected output.
const fixtureJSON = `[
	{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","cca3":"TST","ccn3":"001","capital":["Test City"],"region":"Europe","subregion":"Northern Europe","currencies":{"TST":{"name":"Test dollar","symbol":"$"}},"languages":{"eng":"English"},"timezones":["UTC"],"latlng":[0,0]},
	{"name":{"common":"Otherland","official":"Republic of Otherland"},"cca2":"OL","cca3":"OTH","ccn3":"002","capital":["Other City"],"region":"Asia","subregion":"Eastern Asia","currencies":{"OTH":{"name":"Other yen","symbol":"¥"}},"languages":{"oth":"Otherish"},"timezones":["UTC+1"],"latlng":[10,10]},
	{"name":{"common":"NoCoords","official":"Republic of NoCoords"},"cca2":"NC","cca3":"NOC","capital":["Nowhere"],"timezones":[]}
]`

func newFixtureService(t *testing.T) *Service {
	t.Helper()
	svc, err := NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("NewService() unexpected error: %v", err)
	}
	return svc
}

func TestNewService_Success(t *testing.T) {
	svc := newFixtureService(t)
	if svc.Count() != 3 {
		t.Errorf("Count() = %d, want 3", svc.Count())
	}
}

// Malformed JSON must be rejected rather than producing a half-built service.
func TestNewService_InvalidJSON(t *testing.T) {
	_, err := NewService([]byte("not json"))
	if err == nil {
		t.Error("NewService() with invalid JSON expected error, got nil")
	}
}

func TestGetAll(t *testing.T) {
	svc := newFixtureService(t)
	all := svc.GetAll()
	if len(all) != 3 {
		t.Errorf("GetAll() len = %d, want 3", len(all))
	}
}

// GetByCode must be case-insensitive regardless of how the code was
// originally cased in the source data or the query.
func TestGetByCode(t *testing.T) {
	svc := newFixtureService(t)

	if got := svc.GetByCode("tl"); got == nil || got.Name.Common != "Testland" {
		t.Errorf("GetByCode(tl) = %+v, want Testland", got)
	}
	if got := svc.GetByCode("TL"); got == nil || got.Name.Common != "Testland" {
		t.Errorf("GetByCode(TL) = %+v, want Testland", got)
	}
	if got := svc.GetByCode("ol"); got == nil || got.Name.Common != "Otherland" {
		t.Errorf("GetByCode(ol) = %+v, want Otherland", got)
	}
	if got := svc.GetByCode("001"); got == nil || got.Name.Common != "Testland" {
		t.Errorf("GetByCode(001) = %+v, want Testland", got)
	}
	if got := svc.GetByCode("zz"); got != nil {
		t.Errorf("GetByCode(zz) = %+v, want nil", got)
	}
}

func TestSearchFiltered(t *testing.T) {
	svc := newFixtureService(t)
	tests := []struct {
		name   string
		filter SearchFilters
		want   string
	}{
		{"alpha-3 code", SearchFilters{Code: "tst"}, "Testland"},
		{"numeric code", SearchFilters{Query: "001"}, "Testland"},
		{"region", SearchFilters{Region: "northern"}, "Testland"},
		{"currency", SearchFilters{Currency: "test dollar"}, "Testland"},
		{"language", SearchFilters{Language: "eng"}, "Testland"},
		{"combined filters", SearchFilters{Region: "europe", Language: "English"}, "Testland"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := svc.SearchFiltered(test.filter)
			if len(got) != 1 || got[0].Name.Common != test.want {
				t.Fatalf("SearchFiltered(%+v) = %+v, want [%s]", test.filter, got, test.want)
			}
		})
	}
	if got := svc.SearchFiltered(SearchFilters{Currency: "missing"}); len(got) != 0 {
		t.Errorf("missing currency filter = %+v, want no matches", got)
	}
}

func TestSearch(t *testing.T) {
	svc := newFixtureService(t)

	if got := svc.Search("test"); len(got) != 1 || got[0].Name.Common != "Testland" {
		t.Errorf("Search(test) = %+v, want [Testland]", got)
	}
	// Case-insensitivity.
	if got := svc.Search("TESTLAND"); len(got) != 1 {
		t.Errorf("Search(TESTLAND) = %+v, want 1 match", got)
	}
	if got := svc.Search("nomatch"); len(got) != 0 {
		t.Errorf("Search(nomatch) = %+v, want no matches", got)
	}
	// An empty query is a substring of everything.
	if got := svc.Search(""); len(got) != 3 {
		t.Errorf("Search(\"\") = %+v, want all 3 countries", got)
	}
}

// FindNearest on an empty service must return nil rather than panicking.
func TestFindNearest_Empty(t *testing.T) {
	svc, err := NewService([]byte(`[]`))
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	if got := svc.FindNearest(0, 0); got != nil {
		t.Errorf("FindNearest() on empty service = %+v, want nil", got)
	}
}

// Countries lacking usable LatLng must be skipped, and the closest of the
// remaining countries returned with a zero distance for an exact match.
func TestFindNearest(t *testing.T) {
	svc := newFixtureService(t)

	got := svc.FindNearest(0, 0)
	if got == nil {
		t.Fatal("FindNearest(0,0) = nil, want Testland")
	}
	if got.Name.Common != "Testland" {
		t.Errorf("FindNearest(0,0).Name.Common = %q, want Testland", got.Name.Common)
	}
	if got.Distance != 0 {
		t.Errorf("FindNearest(0,0).Distance = %v, want 0", got.Distance)
	}

	got2 := svc.FindNearest(9, 9)
	if got2 == nil || got2.Name.Common != "Otherland" {
		t.Errorf("FindNearest(9,9) = %+v, want Otherland", got2)
	}
}

func TestFindNearby(t *testing.T) {
	svc := newFixtureService(t)

	// A tiny radius around (0,0) should only capture Testland.
	got := svc.FindNearby(0, 0, 100)
	if len(got) != 1 || got[0].Name.Common != "Testland" {
		t.Errorf("FindNearby(0,0,100) = %+v, want [Testland]", got)
	}

	// A huge radius should capture both countries with LatLng, sorted
	// nearest-first.
	all := svc.FindNearby(0, 0, 20000)
	if len(all) != 2 {
		t.Fatalf("FindNearby(0,0,20000) len = %d, want 2", len(all))
	}
	if all[0].Name.Common != "Testland" || all[1].Name.Common != "Otherland" {
		t.Errorf("FindNearby() order = [%s %s], want [Testland Otherland]", all[0].Name.Common, all[1].Name.Common)
	}
	if all[0].Distance > all[1].Distance {
		t.Errorf("FindNearby() not sorted ascending: %v > %v", all[0].Distance, all[1].Distance)
	}

	// Zero radius yields no results unless a country is exactly at the point.
	if got := svc.FindNearby(50, 50, 0); len(got) != 0 {
		t.Errorf("FindNearby(50,50,0) = %+v, want empty", got)
	}
}

func TestGetRaw(t *testing.T) {
	svc := newFixtureService(t)
	var decoded []Country
	if err := json.Unmarshal(svc.GetRaw(), &decoded); err != nil {
		t.Fatalf("GetRaw() did not round-trip through json.Unmarshal: %v", err)
	}
	if len(decoded) != 3 {
		t.Errorf("GetRaw() decoded len = %d, want 3", len(decoded))
	}
}

// GetRandom on an empty service must return nil rather than panicking with
// an out-of-range index.
func TestGetRandom_Empty(t *testing.T) {
	svc, err := NewService([]byte(`[]`))
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}
	if got := svc.GetRandom(); got != nil {
		t.Errorf("GetRandom() on empty service = %+v, want nil", got)
	}
}

// GetRandom on a non-empty service must always return one of the actual
// countries (a valid index), never nil or an out-of-range panic.
func TestGetRandom(t *testing.T) {
	svc := newFixtureService(t)
	for i := 0; i < 20; i++ {
		got := svc.GetRandom()
		if got == nil {
			t.Fatal("GetRandom() = nil, want a country")
		}
		found := false
		for _, c := range svc.GetAll() {
			if c.CCA2 == got.CCA2 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("GetRandom() returned %+v, not present in service data", got)
		}
	}
}

// haversine of a point against itself must be exactly zero, and a known
// distance (roughly London to Paris) must be within a small tolerance of
// the well-known ~344km value.
func TestHaversine(t *testing.T) {
	if d := haversine(51.5074, -0.1278, 51.5074, -0.1278); d != 0 {
		t.Errorf("haversine(same point) = %v, want 0", d)
	}

	got := haversine(51.5074, -0.1278, 48.8566, 2.3522)
	want := 344.0
	if math.Abs(got-want) > 5 {
		t.Errorf("haversine(London, Paris) = %v, want ~%v", got, want)
	}
}
